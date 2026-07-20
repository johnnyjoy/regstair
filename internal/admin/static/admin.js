(() => {
	const csrfStorageKey = "regstair-admin-csrf";
	const setupForm = document.querySelector("#setup-form");
	if (setupForm) {
		const theme = document.querySelector("#setup-theme");
		const media = window.matchMedia("(prefers-color-scheme: dark)");
		const applySetupTheme = () => {
			const preference = theme.value;
			const resolved = preference === "system" ? (media.matches ? "dark" : "light") : preference;
			document.documentElement.dataset.theme = resolved;
			document.documentElement.style.colorScheme = resolved;
		};
		theme.addEventListener("change", applySetupTheme);
		media.addEventListener("change", applySetupTheme);
		applySetupTheme();
		setupForm.addEventListener("submit", async (event) => {
			event.preventDefault();
			const result = document.querySelector("#setup-result");
			const submit = setupForm.querySelector('button[type="submit"]');
			const password = setupForm.elements.password.value;
			if (password !== setupForm.elements.confirm_password.value) {
				result.textContent = "The passwords do not match.";
				setupForm.elements.confirm_password.focus();
				return;
			}
			submit.disabled = true;
			result.textContent = "Creating the control-plane administrator...";
			try {
				const response = await fetch("/admin/api/setup", {method: "POST", headers: {"Content-Type": "application/json", "X-Regstair-Setup-Token": setupForm.dataset.setupToken}, body: JSON.stringify({username: setupForm.elements.username.value, password, display_name: setupForm.elements.display_name.value, email: setupForm.elements.email.value})});
				setupForm.elements.password.value = "";
				setupForm.elements.confirm_password.value = "";
				const payload = await response.json();
				if (!response.ok) throw new Error(payload?.error?.message || "Setup could not be completed.");
				window.sessionStorage.setItem(csrfStorageKey, payload.csrf_token);
				window.location.assign("/");
			} catch (error) {
				result.textContent = error.message || "Setup could not be completed.";
				submit.disabled = false;
				setupForm.elements.username.focus();
			}
		});
		return;
	}
	const loginForm = document.querySelector("#login-form");
	if (loginForm) {
		loginForm.addEventListener("submit", async (event) => {
			event.preventDefault();
			const submit = loginForm.querySelector('button[type="submit"]');
			const result = document.querySelector("#login-result");
			submit.disabled = true;
			result.textContent = "Signing in...";
			const form = new FormData(loginForm);
			try {
				const response = await fetch("/admin/api/login", {method: "POST", headers: {"Content-Type": "application/json"}, body: JSON.stringify({username: form.get("username"), password: form.get("password")})});
				const payload = await response.json();
				if (!response.ok) throw new Error("The username or password was not accepted.");
				window.sessionStorage.setItem(csrfStorageKey, payload.csrf_token);
				window.location.assign("/");
			} catch (error) {
				result.textContent = error.message || "Sign in failed.";
				submit.disabled = false;
				loginForm.elements.username.focus();
			}
		});
		return;
	}

  const storageKey = "regstair-admin-theme";
  const selector = document.querySelector("#theme");
  const status = document.querySelector("#theme-status");
  const systemTheme = window.matchMedia("(prefers-color-scheme: dark)");

  const storedTheme = () => {
    try {
      const value = window.localStorage.getItem(storageKey);
      return value === "light" || value === "dark" ? value : "system";
    } catch {
      return "system";
    }
  };

  const applyTheme = (preference, announce) => {
    const resolved = preference === "system" ? (systemTheme.matches ? "dark" : "light") : preference;
    document.documentElement.dataset.theme = resolved;
    document.documentElement.style.colorScheme = resolved;
    selector.value = preference;
    if (announce) status.textContent = `Theme changed to ${preference}.`;
  };

  selector.addEventListener("change", () => {
    const preference = selector.value;
    try {
      if (preference === "system") window.localStorage.removeItem(storageKey);
      else window.localStorage.setItem(storageKey, preference);
    } catch {
      // The selected theme still applies when browser storage is unavailable.
    }
    applyTheme(preference, true);
  });

  systemTheme.addEventListener("change", () => {
    if (selector.value === "system") applyTheme("system", false);
  });

	applyTheme(storedTheme(), false);

	const cookieValue = (name) => document.cookie.split(";").map((part) => part.trim()).find((part) => part.startsWith(`${name}=`))?.slice(name.length + 1) || "";
	const csrfToken = () => decodeURIComponent(cookieValue("regstair_csrf")) || window.sessionStorage.getItem(csrfStorageKey) || "";
	const mutationNotice = document.querySelector("#mutation-notice");
	const showMutationError = (message, response) => {
		mutationNotice.hidden = false;
		mutationNotice.querySelector("strong").textContent = response?.status === 401 || response?.status === 403 ? "Your session needs attention" : "The action could not be completed";
		mutationNotice.querySelector("p").textContent = response?.status === 401 || response?.status === 403 ? "Sign in again, then repeat the action. No changes were applied." : message;
		mutationNotice.querySelector("a").hidden = !(response?.status === 401 || response?.status === 403);
		const reload = mutationNotice.querySelector("button"); reload.hidden = response?.status === 401 || response?.status === 403; reload.onclick = () => window.location.reload();
		mutationNotice.focus?.();
	};
	const apiFetch = (url, options = {}) => {
		const headers = new Headers(options.headers || {});
		if ((options.method || "GET") !== "GET") headers.set("X-CSRF-Token", csrfToken());
		return fetch(url, {...options, headers});
	};
	const confirmDialog = document.querySelector("#confirm-dialog");
	const confirmAction = (title, message, actionLabel) => new Promise((resolve) => {
		confirmDialog.querySelector("#confirm-title").textContent = title;
		confirmDialog.querySelector("#confirm-message").textContent = message;
		const action = confirmDialog.querySelector("#confirm-action"); action.textContent = actionLabel;
		const finish = (accepted) => { confirmDialog.close(); document.body.classList.remove("modal-open"); resolve(accepted); };
		action.onclick = () => finish(true);
		confirmDialog.querySelectorAll(".confirm-cancel").forEach((button) => { button.onclick = () => finish(false); });
		confirmDialog.oncancel = (event) => { event.preventDefault(); finish(false); };
		confirmDialog.showModal(); document.body.classList.add("modal-open"); action.focus();
	});

	const dialog = document.querySelector("#credential-dialog");
	if (dialog) {
	const form = document.querySelector("#credential-form");
	const result = document.querySelector("#credential-result");
	const title = document.querySelector("#credential-dialog-title");
	const sourceLabel = document.querySelector("#credential-dialog-source");
	let opener = null;

	const closeDialog = () => {
		dialog.close();
		document.body.classList.remove("modal-open");
		form.reset();
		result.textContent = "";
		if (opener) opener.focus();
	};
	document.querySelectorAll(".dialog-close").forEach((button) => button.addEventListener("click", closeDialog));
	dialog.addEventListener("cancel", (event) => { event.preventDefault(); closeDialog(); });

	document.querySelectorAll(".credential-open").forEach((button) => button.addEventListener("click", () => {
		opener = button;
		const item = button.closest(".credential-item");
		form.elements.source_id.value = item.dataset.sourceId;
		title.textContent = button.textContent.trim();
		sourceLabel.textContent = `${item.dataset.sourceName || item.dataset.sourceId} (${item.dataset.sourceId})`;
		dialog.showModal();
		document.body.classList.add("modal-open");
		form.elements.username.focus();
	}));

	const secretToggle = document.querySelector(".secret-toggle");
	secretToggle.addEventListener("click", () => {
		const input = form.elements.secret;
		const reveal = input.type === "password";
		input.type = reveal ? "text" : "password";
		secretToggle.textContent = reveal ? "Hide" : "Show";
		secretToggle.setAttribute("aria-label", reveal ? "Hide secret" : "Show secret");
		secretToggle.setAttribute("aria-pressed", String(reveal));
		input.focus();
	});

	form.addEventListener("submit", async (event) => {
		event.preventDefault();
		const submit = form.querySelector('button[type="submit"]');
		submit.disabled = true;
		result.className = "form-result pending";
		result.textContent = "Verifying access with the registry...";
		const sourceID = form.elements.source_id.value;
		try {
			const response = await apiFetch(`/admin/api/account/registry-credentials/${encodeURIComponent(sourceID)}/verify-and-save`, {method: "POST", headers: {"Content-Type": "application/json"}, body: JSON.stringify({username: form.elements.username.value, secret: form.elements.secret.value})});
			form.elements.secret.value = "";
			const payload = await response.json();
			if (!response.ok) throw new Error(payload.error?.message || "The credential could not be verified.");
			result.className = "form-result success-message";
			result.textContent = "Credential verified and saved.";
			setTimeout(() => window.location.reload(), 500);
		} catch (error) {
			form.elements.secret.value = "";
			result.className = "form-result error-message";
			result.textContent = error.message || "The credential could not be verified.";
			submit.disabled = false;
			form.elements.secret.focus();
		}
	});

	document.querySelectorAll(".credential-remove").forEach((button) => button.addEventListener("click", async () => {
		const item = button.closest(".credential-item");
		if (!await confirmAction("Remove registry credential", `Remove the saved credential for ${item.dataset.sourceName || item.dataset.sourceId}? Pulls and pushes requiring it may fail immediately.`, "Remove credential")) return;
		button.disabled = true;
		const response = await apiFetch(`/admin/api/account/registry-credentials/${encodeURIComponent(item.dataset.sourceId)}`, {method: "DELETE", headers: {"Content-Type": "application/json"}, body: JSON.stringify({confirm: true})});
		if (response.ok) window.location.reload();
		else { button.disabled = false; showMutationError("The credential could not be removed. Reload the page and try again.", response); }
	}));
	}

	const logout = document.querySelector("#logout");
	if (logout) logout.addEventListener("click", async () => {
		logout.disabled = true;
		const response = await apiFetch("/admin/api/logout", {method: "POST"});
		if (response.ok) { window.sessionStorage.removeItem(csrfStorageKey); window.location.assign("/login"); }
		else { logout.disabled = false; showMutationError("Sign out could not be completed.", response); }
	});

	const passwordForm = document.querySelector("#password-change-form");
	if (passwordForm) passwordForm.addEventListener("submit", async (event) => { event.preventDefault(); const submit = passwordForm.querySelector('button[type="submit"]'); const status = passwordForm.querySelector(".form-result"); submit.disabled = true; const response = await apiFetch("/admin/api/account/password", {method: "POST", headers: {"Content-Type": "application/json"}, body: JSON.stringify({current_password: passwordForm.elements.current_password.value, new_password: passwordForm.elements.new_password.value})}); passwordForm.reset(); if (response.ok) { window.sessionStorage.removeItem(csrfStorageKey); window.location.assign("/login"); } else { status.className = "form-result error-message"; status.textContent = "The password could not be changed."; submit.disabled = false; showMutationError("Check the current password and try again.", response); } });

	const tokenDialog = document.querySelector("#token-create-dialog");
	if (tokenDialog) {
		const tokenForm = document.querySelector("#token-create-form"); const tokenOpen = document.querySelector("#token-create-open"); const secret = document.querySelector("#token-secret-result"); const retained = document.querySelector("#token-retained"); let tokenNeedsAcknowledgment = false;
		const closeToken = () => { if (tokenNeedsAcknowledgment && !retained.checked) { const status = tokenForm.querySelector(".form-result"); status.className = "form-result error-message"; status.textContent = "Acknowledge that the token is stored before closing."; retained.focus(); return; } tokenNeedsAcknowledgment = false; secret.querySelector("code").textContent = ""; tokenDialog.close(); document.body.classList.remove("modal-open"); tokenForm.reset(); secret.hidden = true; const submit = tokenForm.querySelector('button[type="submit"]'); submit.hidden = false; submit.disabled = false; tokenOpen.focus(); };
		tokenOpen.addEventListener("click", () => { tokenDialog.showModal(); document.body.classList.add("modal-open"); tokenForm.elements.label.focus(); });
		document.querySelectorAll(".token-dialog-close").forEach((button) => button.addEventListener("click", closeToken));
		tokenDialog.addEventListener("cancel", (event) => { event.preventDefault(); closeToken(); });
		document.querySelector("#token-copy").addEventListener("click", async () => { const status = tokenForm.querySelector(".form-result"); try { await navigator.clipboard.writeText(secret.querySelector("code").textContent); status.className = "form-result success-message"; status.textContent = "Token copied. Store it before closing."; } catch { status.className = "form-result error-message"; status.textContent = "Clipboard access was unavailable. Select and copy the token manually."; } });
		window.addEventListener("beforeunload", (event) => { if (tokenNeedsAcknowledgment && !retained.checked) { event.preventDefault(); event.returnValue = ""; } });
		tokenForm.addEventListener("submit", async (event) => { event.preventDefault(); const submit = tokenForm.querySelector('button[type="submit"]'); const status = tokenForm.querySelector(".form-result"); submit.disabled = true; status.textContent = "Creating token..."; const response = await apiFetch("/admin/api/account/docker-tokens", {method: "POST", headers: {"Content-Type": "application/json"}, body: JSON.stringify({label: tokenForm.elements.label.value, expires_in_days: Number(tokenForm.elements.expires_in_days.value)})}); const payload = await response.json(); if (response.ok) { secret.querySelector("code").textContent = payload.secret; secret.hidden = false; tokenNeedsAcknowledgment = true; status.className = "form-result success-message"; status.textContent = "Token created."; submit.hidden = true; document.querySelector("#token-copy").focus(); } else { status.className = "form-result error-message"; status.textContent = "The token could not be created."; submit.disabled = false; showMutationError("The token could not be created.", response); } });
	}
	document.querySelectorAll(".token-revoke").forEach((button) => button.addEventListener("click", async () => { if (!await confirmAction("Revoke Docker token", "This token will stop working immediately. Running clients using it will need a replacement token.", "Revoke token")) return; button.disabled = true; const response = await apiFetch(`/admin/api/account/docker-tokens/${encodeURIComponent(button.dataset.tokenId)}`, {method: "DELETE"}); if (response.ok) window.location.reload(); else { button.disabled = false; showMutationError("The token could not be revoked.", response); } }));

	document.querySelectorAll(".user-save").forEach((button) => button.addEventListener("click", async () => {
		const row = button.closest(".user-row");
		const nextAccess = row.querySelector(".user-access").value; const nextEnabled = row.querySelector(".user-enabled").checked; const changes = [];
		if (nextAccess !== row.dataset.currentAccess) changes.push(`Role: ${row.dataset.currentAccess} to ${nextAccess}`);
		if (String(nextEnabled) !== row.dataset.currentEnabled) changes.push(`State: ${row.dataset.currentEnabled === "true" ? "enabled" : "disabled"} to ${nextEnabled ? "enabled" : "disabled"}`);
		if (!changes.length) { showMutationError("Choose a different role or enabled state before reviewing changes."); return; }
		const consequence = nextAccess !== row.dataset.currentAccess || !nextEnabled ? " Existing web sessions and Docker access are invalidated immediately." : "";
		if (!await confirmAction(`Apply changes to ${row.dataset.username}`, `${changes.join(". ")}.${consequence}`, "Apply changes")) return;
		button.disabled = true;
		const response = await apiFetch(`/admin/api/users/${encodeURIComponent(row.dataset.userId)}`, {method: "PATCH", headers: {"Content-Type": "application/json"}, body: JSON.stringify({access: row.querySelector(".user-access").value, enabled: row.querySelector(".user-enabled").checked, mtime: row.dataset.mtime})});
		if (response.ok) window.location.reload();
		else { button.disabled = false; showMutationError("The user could not be updated. Reload the page and try again.", response); }
	}));

	const createDialog = document.querySelector("#user-create-dialog");
	if (createDialog) {
		const createForm = document.querySelector("#user-create-form");
		const closeCreate = () => { createDialog.close(); document.body.classList.remove("modal-open"); createForm.reset(); document.querySelector("#user-create-open").focus(); };
		document.querySelector("#user-create-open").addEventListener("click", () => { createDialog.showModal(); document.body.classList.add("modal-open"); createForm.elements.username.focus(); });
		document.querySelectorAll(".user-dialog-close").forEach((button) => button.addEventListener("click", closeCreate));
		createDialog.addEventListener("cancel", (event) => { event.preventDefault(); closeCreate(); });
		createForm.addEventListener("submit", async (event) => {
			event.preventDefault(); const submit = createForm.querySelector('button[type="submit"]'); const status = createForm.querySelector(".form-result"); submit.disabled = true; status.textContent = "Creating user...";
			const values = new FormData(createForm);
			const response = await apiFetch("/admin/api/users", {method: "POST", headers: {"Content-Type": "application/json"}, body: JSON.stringify({username: values.get("username"), password: values.get("password"), display_name: values.get("display_name"), email: values.get("email"), access: values.get("access"), enabled: values.get("enabled") === "on"})});
			createForm.elements.password.value = "";
			if (response.ok) window.location.reload(); else { status.className = "form-result error-message"; status.textContent = "The user could not be created."; submit.disabled = false; showMutationError("Review the user details and try again.", response); }
		});
	}

	const resetDialog = document.querySelector("#user-reset-dialog");
	if (resetDialog) {
		const resetForm = document.querySelector("#user-reset-form"); let resetOpener = null;
		const closeReset = () => { resetDialog.close(); document.body.classList.remove("modal-open"); resetForm.reset(); if (resetOpener) resetOpener.focus(); };
		document.querySelectorAll(".user-reset-open").forEach((button) => button.addEventListener("click", () => { resetOpener = button; resetForm.elements.user_id.value = button.closest(".user-row").dataset.userId; document.querySelector("#user-reset-title").nextElementSibling.textContent = button.dataset.username; resetDialog.showModal(); document.body.classList.add("modal-open"); resetForm.elements.password.focus(); }));
		document.querySelectorAll(".reset-dialog-close").forEach((button) => button.addEventListener("click", closeReset));
		resetDialog.addEventListener("cancel", (event) => { event.preventDefault(); closeReset(); });
		resetForm.addEventListener("submit", async (event) => { event.preventDefault(); const submit = resetForm.querySelector('button[type="submit"]'); submit.disabled = true; const response = await apiFetch(`/admin/api/users/${encodeURIComponent(resetForm.elements.user_id.value)}/password`, {method: "POST", headers: {"Content-Type": "application/json"}, body: JSON.stringify({password: resetForm.elements.password.value})}); resetForm.elements.password.value = ""; if (response.ok) window.location.reload(); else { resetForm.querySelector(".form-result").textContent = "The password could not be reset."; submit.disabled = false; showMutationError("The password could not be reset.", response); } });
	}
})();
