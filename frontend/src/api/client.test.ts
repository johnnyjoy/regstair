import { afterEach, describe, expect, it, vi } from "vitest";

import { SessionExpiredError, apiRequest } from "./client";

afterEach(() => {
  vi.unstubAllGlobals();
  document.cookie = "regstair_csrf=; Max-Age=0; Path=/";
});

describe("apiRequest", () => {
  it("uses the current session for same-origin JSON reads", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ healthy: true }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
    vi.stubGlobal("fetch", fetchMock);

    await expect(apiRequest<{ healthy: boolean }>("/admin/api/health")).resolves.toEqual({ healthy: true });

    expect(fetchMock).toHaveBeenCalledWith(
      "/admin/api/health",
      expect.objectContaining({
        credentials: "same-origin",
        headers: expect.any(Headers),
      }),
    );
    const headers = fetchMock.mock.calls[0][1].headers as Headers;
    expect(headers.get("Accept")).toBe("application/json");
  });

  it("adds the decoded CSRF cookie to mutations", async () => {
    document.cookie = "regstair_csrf=token%2Fwith%2Bencoding; Path=/";
    const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 204 }));
    vi.stubGlobal("fetch", fetchMock);

    await apiRequest("/admin/api/logout", { method: "POST" });

    const headers = fetchMock.mock.calls[0][1].headers as Headers;
    expect(headers.get("X-CSRF-Token")).toBe("token/with+encoding");
  });

  it("classifies an expired session separately", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(null, { status: 401 })));

    await expect(apiRequest("/admin/api/account")).rejects.toBeInstanceOf(SessionExpiredError);
  });

  it("preserves a safe API error classification", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response(JSON.stringify({ error: "credential_verification_failed" }), {
          status: 422,
          headers: { "Content-Type": "application/json" },
        }),
      ),
    );

    await expect(apiRequest("/admin/api/account/registry-credentials/harbor")).rejects.toEqual(
      expect.objectContaining({ status: 422, classification: "credential_verification_failed" }),
    );
  });
});
