/*
 * discord.js — completes the "Sign in with Discord" flow (discord.html).
 *
 * The backend OAuth callback redirects here with the freshly issued tokens in
 * the URL fragment (so they never reach a server or a log). We read them, store
 * the session, clear the fragment from history, and continue into the app.
 */

(function () {
  // The fragment looks like "#token=...&refresh_token=...&expires_in=...".
  const hash = window.location.hash.startsWith("#")
    ? window.location.hash.slice(1)
    : "";
  const params = new URLSearchParams(hash);
  const token = params.get("token");
  const refreshToken = params.get("refresh_token");

  if (token && refreshToken) {
    api.setSession({ token, refresh_token: refreshToken });
    // Drop the tokens from the address bar / history before navigating on.
    window.history.replaceState({}, "", window.location.pathname);
    window.location.replace("index.html");
    return;
  }

  // Missing tokens — send the user back to the login page with a generic error.
  window.location.replace("login.html?discord_error=server_error");
})();
