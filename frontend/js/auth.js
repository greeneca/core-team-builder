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

  // Hide the Register tab when an admin has disabled self-registration. The
  // backend still enforces this; this is just to avoid a dead-end form.
  api
    .registrationStatus()
    .then((status) => {
      if (status && status.enabled === false && registerTab) {
        registerTab.classList.add("is-hidden");
        registerForm.classList.add("is-hidden");
        activateTab("login");
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
