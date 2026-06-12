/*
 * reset.js — drives the password-reset page (reset.html).
 *
 * Reads the single-use token from the URL query string (?token=...), collects a
 * new password, and completes the reset via the backend. On success the user is
 * directed back to sign in. Resetting revokes all existing sessions server-side.
 */

(function () {
  const params = new URLSearchParams(window.location.search);
  const token = params.get("token");

  const form = document.getElementById("reset-form");
  const message = document.getElementById("message");

  function showMessage(text, kind = "error") {
    message.textContent = text;
    message.className = `message message--${kind}`;
  }

  function clearMessage() {
    message.textContent = "";
    message.className = "message is-hidden";
  }

  // Without a token the link is malformed — hide the form and explain.
  if (!token) {
    form.classList.add("is-hidden");
    showMessage(
      "This reset link is missing its token. Please request a new password reset."
    );
    return;
  }

  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    clearMessage();
    const password = document.getElementById("reset-password").value;
    const confirm = document.getElementById("reset-password-confirm").value;

    if (password !== confirm) {
      showMessage("Passwords do not match.");
      return;
    }

    try {
      const data = await api.resetPassword(token, password);
      form.classList.add("is-hidden");
      showMessage(
        (data && data.message) ||
          "Your password has been reset. You can now sign in.",
        "success"
      );
      // Send the user back to sign in after a short pause so they can read the
      // confirmation.
      setTimeout(() => window.location.replace("login.html"), 2500);
    } catch (err) {
      showMessage(err.message);
    }
  });
})();
