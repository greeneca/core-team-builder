/*
 * app.js — drives the authenticated dashboard (index.html).
 *
 * Guards the page behind authentication, loads the current user, and wires up
 * the sign-out button.
 */

(function () {
  // Route guard: bounce unauthenticated visitors back to login.
  if (!api.isAuthenticated()) {
    window.location.replace("login.html");
    return;
  }

  const logoutBtn = document.getElementById("logout-btn");
  logoutBtn.addEventListener("click", () => {
    api.clearToken();
    window.location.replace("login.html");
  });

  async function loadUser() {
    try {
      const user = await api.me();
      document.getElementById("username").textContent = user.username;
      document.getElementById("email").textContent = user.email;
      const created = new Date(user.created_at);
      document.getElementById("created-at").textContent = created.toLocaleDateString(
        undefined,
        { year: "numeric", month: "long", day: "numeric" }
      );
    } catch (err) {
      // Token expired or invalid — force a fresh login.
      if (err.status === 401) {
        api.clearToken();
        window.location.replace("login.html");
        return;
      }
      console.error("failed to load user", err);
    }
  }

  loadUser();
})();
