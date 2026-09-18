package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/core-team-builder/backend/internal/auth"
	"github.com/core-team-builder/backend/internal/db"
	"github.com/core-team-builder/backend/internal/models"
	"github.com/core-team-builder/backend/internal/realtime"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The tests in this package's *_integration_test.go files run the real routing,
// middleware, handler, store, and SQL stack against a live PostgreSQL. They are
// opt-in: set TEST_DATABASE_URL to a database the tests may freely destroy
// (every table is truncated between tests) and they run; leave it unset and
// they skip, so `go test ./...` still works with nothing installed.
//
//	createdb ctb_test
//	TEST_DATABASE_URL=postgres://localhost/ctb_test?sslmode=disable go test ./internal/handlers/
//
// TEST_MIGRATIONS_DIR overrides where the schema is built from; it defaults to
// the repo's database/migrations.
const (
	databaseURLEnv   = "TEST_DATABASE_URL"
	migrationsDirEnv = "TEST_MIGRATIONS_DIR"
)

var (
	// The schema is built once per package run and shared; individual tests get
	// isolation from truncateAll instead, which is far cheaper than re-running
	// every migration.
	schemaOnce sync.Once
	schemaPool *pgxpool.Pool
	schemaErr  error
)

// migrationsDir resolves the directory holding the *.sql schema files.
func migrationsDir() string {
	if dir := os.Getenv(migrationsDirEnv); dir != "" {
		return dir
	}
	return filepath.Join("..", "..", "..", "database", "migrations")
}

// requireDB returns a pool against the test database with the schema applied,
// or skips the test when TEST_DATABASE_URL is unset.
func requireDB(t *testing.T) *pgxpool.Pool {
	t.Helper()

	url := os.Getenv(databaseURLEnv)
	if url == "" {
		t.Skipf("%s is not set; skipping the database integration tests", databaseURLEnv)
	}

	schemaOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		schemaPool, schemaErr = db.Connect(ctx, url)
		if schemaErr != nil {
			return
		}
		// Start from nothing so a database left over from an earlier run (or a
		// half-applied schema) can't make the results depend on history.
		if _, schemaErr = schemaPool.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public;`); schemaErr != nil {
			return
		}
		_, schemaErr = db.Migrate(ctx, schemaPool, migrationsDir())
	})
	if schemaErr != nil {
		t.Fatalf("prepare test database: %v", schemaErr)
	}

	truncateAll(t, schemaPool)
	return schemaPool
}

// truncateAll empties every table in the public schema so each test starts from
// an empty database. RESTART IDENTITY also resets the sequences, keeping IDs
// predictable across tests.
func truncateAll(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	rows, err := pool.Query(ctx, `SELECT tablename FROM pg_tables WHERE schemaname = 'public'`)
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan table name: %v", err)
		}
		tables = append(tables, `"`+name+`"`)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("list tables: %v", err)
	}
	if len(tables) == 0 {
		t.Fatal("the test database has no tables — did the migrations apply?")
	}

	stmt := "TRUNCATE " + strings.Join(tables, ", ") + " RESTART IDENTITY CASCADE"
	if _, err := pool.Exec(ctx, stmt); err != nil {
		t.Fatalf("truncate tables: %v", err)
	}
}

// captureMailer records the messages the password-reset flow sends instead of
// delivering them. Sending happens on a goroutine, so sent is buffered and
// waitForMail blocks until the message lands.
type captureMailer struct {
	sent chan sentMail
}

type sentMail struct {
	to      string
	subject string
	body    string
}

func newCaptureMailer() *captureMailer {
	return &captureMailer{sent: make(chan sentMail, 8)}
}

func (m *captureMailer) Send(_ context.Context, to, subject, body string) error {
	m.sent <- sentMail{to: to, subject: subject, body: body}
	return nil
}

// waitForMail returns the next captured message, failing the test if none
// arrives.
func (m *captureMailer) waitForMail(t *testing.T) sentMail {
	t.Helper()
	select {
	case msg := <-m.sent:
		return msg
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for an email to be sent")
		return sentMail{}
	}
}

// testAPI is a running instance of the full API backed by the test database.
type testAPI struct {
	t      *testing.T
	server *httptest.Server
	pool   *pgxpool.Pool
	mailer *captureMailer
}

// newTestAPI wires the real stores, token manager, and routes over the test
// database and serves them on a local listener.
func newTestAPI(t *testing.T) *testAPI {
	t.Helper()

	pool := requireDB(t)
	mailer := newCaptureMailer()

	srv := New(Config{
		Users:            models.NewUserStore(pool),
		Teams:            models.NewTeamStore(pool),
		Rosters:          models.NewRosterStore(pool),
		RosterImages:     models.NewRosterImageStore(pool),
		Encounters:       models.NewEncounterStore(pool),
		Groupings:        models.NewGroupingStore(pool),
		Members:          models.NewMemberStore(pool),
		Settings:         models.NewSettingsStore(pool),
		RefreshTokens:    models.NewRefreshTokenStore(pool),
		PasswordResets:   models.NewPasswordResetStore(pool),
		Discord:          models.NewDiscordStore(pool),
		Tokens:           auth.NewTokenManager([]byte("integration-test-secret"), 15*time.Minute, 24*time.Hour),
		Mailer:           mailer,
		Realtime:         realtime.NewHub(),
		CORSOrigin:       "http://localhost",
		AppBaseURL:       "http://localhost",
		PasswordResetTTL: time.Hour,
	})

	httpSrv := httptest.NewServer(srv.Routes())
	t.Cleanup(httpSrv.Close)

	return &testAPI{t: t, server: httpSrv, pool: pool, mailer: mailer}
}

// response is a decoded HTTP response, with helpers for the assertions the
// tests repeat.
type response struct {
	t      *testing.T
	status int
	body   []byte
}

// do issues a request against the test server. A nil body sends none; token,
// when non-empty, is sent as the bearer credential.
func (a *testAPI) do(method, path, token string, body any) *response {
	a.t.Helper()

	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			a.t.Fatalf("encode request body: %v", err)
		}
		reader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequest(method, a.server.URL+path, reader)
	if err != nil {
		a.t.Fatalf("build request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := a.server.Client().Do(req)
	if err != nil {
		a.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		a.t.Fatalf("read %s %s response: %v", method, path, err)
	}
	return &response{t: a.t, status: resp.StatusCode, body: raw}
}

// expect asserts the status code and returns the response for further checks.
func (r *response) expect(status int) *response {
	r.t.Helper()
	if r.status != status {
		r.t.Fatalf("status = %d, want %d (body: %s)", r.status, status, strings.TrimSpace(string(r.body)))
	}
	return r
}

// decode unmarshals the JSON body into v.
func (r *response) decode(v any) {
	r.t.Helper()
	if err := json.Unmarshal(r.body, v); err != nil {
		r.t.Fatalf("decode response: %v (body: %s)", err, strings.TrimSpace(string(r.body)))
	}
}

// errorMessage returns the `error` field of a failure response.
func (r *response) errorMessage() string {
	r.t.Helper()
	var payload struct {
		Error string `json:"error"`
	}
	r.decode(&payload)
	return payload.Error
}

// session is a registered user plus their live credentials.
type session struct {
	user         models.User
	token        string
	refreshToken string
	password     string
}

// registerUser creates an account through the public endpoint and returns the
// resulting session. Names are derived from the caller-supplied suffix so
// several users in one test stay distinct.
func (a *testAPI) registerUser(name string) *session {
	a.t.Helper()

	password := "integration-test-pw-" + name
	resp := a.do(http.MethodPost, "/api/register", "", map[string]string{
		"username": name,
		"email":    fmt.Sprintf("%s@example.test", name),
		"password": password,
	}).expect(http.StatusOK)

	var payload struct {
		Token        string      `json:"token"`
		RefreshToken string      `json:"refresh_token"`
		User         models.User `json:"user"`
	}
	resp.decode(&payload)

	return &session{
		user:         payload.User,
		token:        payload.Token,
		refreshToken: payload.RefreshToken,
		password:     password,
	}
}

// createTeam creates a team owned by the session's user and returns it.
func (a *testAPI) createTeam(s *session, name string) models.Team {
	a.t.Helper()

	resp := a.do(http.MethodPost, "/api/teams", s.token, map[string]any{"name": name}).
		expect(http.StatusCreated)

	var team models.Team
	resp.decode(&team)
	return team
}
