// Login form handler for Mitto auth page

// Get API prefix (injected by server into the page)
function getApiPrefix() {
  return window.mittoApiPrefix || "";
}

document.addEventListener("DOMContentLoaded", function () {
  const form = document.getElementById("loginForm");
  const errorDiv = document.getElementById("error");
  const submitBtn = document.getElementById("submitBtn");

  if (!form || !errorDiv || !submitBtn) {
    console.error("Required form elements not found");
    return;
  }

  form.addEventListener("submit", async function (e) {
    e.preventDefault();

    // Disable form during submission
    submitBtn.disabled = true;
    submitBtn.textContent = "Signing in...";
    errorDiv.classList.add("hidden");

    const username = document.getElementById("username").value;
    const password = document.getElementById("password").value;

    try {
      const response = await fetch(getApiPrefix() + "/api/login", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
        },
        body: JSON.stringify({ username: username, password: password }),
        credentials: "same-origin", // Include cookies in request and accept Set-Cookie
      });

      if (response.ok) {
        // Redirect to main app
        window.location.href = "/";
      } else {
        const data = await response.json().catch(function () {
          return {};
        });
        errorDiv.textContent = data.error || "Invalid username or password";
        errorDiv.classList.remove("hidden");
      }
    } catch (err) {
      errorDiv.textContent = "Network error. Please try again.";
      errorDiv.classList.remove("hidden");
    } finally {
      submitBtn.disabled = false;
      submitBtn.textContent = "Sign In";
    }
  });
});
