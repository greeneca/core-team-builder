/*
 * auth.js — drives the login/register page (login.html).
 *
 * Handles tab switching, form submission, and redirects to the dashboard on
 * successful authentication.
 */

(function () {
  // Already signed in? Skip straight to the app.
  if (api.isAuthenticated()) {
    window.location.replace("index.html");
    return;
  }

  const tabs = document.querySelectorAll(".tab");
  const loginForm = document.getElementById("login-form");
  const registerForm = document.getElementById("register-form");
  const forgotForm = document.getElementById("forgot-form");
  const message = document.getElementById("message");
  const registerTab = document.querySelector('.tab[data-tab="register"]');
  const discordAuth = document.getElementById("discord-auth");

  // Human-readable messages for the error codes the Discord OAuth callback can
  // redirect back with (?discord_error=...).
  const DISCORD_ERRORS = {
    access_denied: "Discord sign-in was cancelled.",
    invalid_state: "Discord sign-in expired or was interrupted. Please try again.",
    invalid_request: "Discord sign-in failed. Please try again.",
    no_email:
      "Your Discord account didn't share an email. Add a verified email to Discord, or register with a password.",
    email_unverified:
      "An account with your Discord email already exists. Sign in with your password, then link Discord from settings.",
    already_linked_other:
      "That account is already linked to a different Discord account. Sign in with your password instead.",
    registration_disabled: "New sign-ups are currently disabled.",
    server_error: "Something went wrong signing in with Discord. Please try again.",
  };

  // Surface a Discord sign-in error passed back as a query parameter, then strip
  // it from the URL so a refresh doesn't repeat the message.
  const params = new URLSearchParams(window.location.search);
  const discordError = params.get("discord_error");
  if (discordError) {
    showMessage(DISCORD_ERRORS[discordError] || DISCORD_ERRORS.server_error);
    params.delete("discord_error");
    const qs = params.toString();
    window.history.replaceState(
      {},
      "",
      window.location.pathname + (qs ? "?" + qs : "")
    );
  }

  // Hide the Register tab when an admin has disabled self-registration, and
  // reveal the Discord sign-in button when the backend has it configured. The
  // backend still enforces both; this is just UX.
  api
    .registrationStatus()
    .then((status) => {
      if (status && status.enabled === false && registerTab) {
        registerTab.classList.add("is-hidden");
        registerForm.classList.add("is-hidden");
        activateTab("login");
      }
      if (status && status.discord_enabled && discordAuth) {
        discordAuth.classList.remove("is-hidden");
      }
    })
    .catch(() => {
      /* Non-fatal: leave the tab visible; the backend still gates registration. */
    });

  function showMessage(text, kind = "error") {
    message.textContent = text;
    message.className = `message message--${kind}`;
  }

  function clearMessage() {
    message.textContent = "";
    message.className = "message is-hidden";
  }

  function activateTab(name) {
    clearMessage();
    tabs.forEach((t) => t.classList.toggle("is-active", t.dataset.tab === name));
    loginForm.classList.toggle("is-hidden", name !== "login");
    registerForm.classList.toggle("is-hidden", name !== "register");
    forgotForm.classList.add("is-hidden");
  }

  // Swap the login form for the forgot-password form (the tabs stay on "Sign
  // In"; this is a sub-view of login, not a third tab).
  function showForgot(show) {
    clearMessage();
    loginForm.classList.toggle("is-hidden", show);
    forgotForm.classList.toggle("is-hidden", !show);
  }

  tabs.forEach((tab) => {
    tab.addEventListener("click", () => activateTab(tab.dataset.tab));
  });

  document.getElementById("forgot-link").addEventListener("click", () => showForgot(true));
  document.getElementById("forgot-back").addEventListener("click", () => showForgot(false));

  loginForm.addEventListener("submit", async (e) => {
    e.preventDefault();
    clearMessage();
    const username = document.getElementById("login-username").value.trim();
    const password = document.getElementById("login-password").value;
    try {
      const data = await api.login(username, password);
      api.setSession(data);
      window.location.replace("index.html");
    } catch (err) {
      showMessage(err.message);
    }
  });

  registerForm.addEventListener("submit", async (e) => {
    e.preventDefault();
    clearMessage();
    const username = document.getElementById("register-username").value.trim();
    const email = document.getElementById("register-email").value.trim();
    const password = document.getElementById("register-password").value;
    try {
      const data = await api.register(username, email, password);
      api.setSession(data);
      window.location.replace("index.html");
    } catch (err) {
      showMessage(err.message);
    }
  });

  forgotForm.addEventListener("submit", async (e) => {
    e.preventDefault();
    clearMessage();
    const email = document.getElementById("forgot-email").value.trim();
    try {
      // The backend always returns a generic message regardless of whether the
      // email is registered (no account enumeration).
      const data = await api.forgotPassword(email);
      showMessage(
        (data && data.message) ||
          "If an account exists for that email, a reset link has been sent.",
        "success"
      );
      forgotForm.reset();
    } catch (err) {
      showMessage(err.message);
    }
  });
})();
