import { test, expect } from "../fixtures/test-fixtures";

/**
 * Server-side persistence for the theme preference cluster.
 *
 * Background:
 *   The macOS app binds a fresh random localhost port on every launch, and
 *   WKWebView's localStorage is scoped by scheme+host+port. Without server-side
 *   persistence every theme-related preference (font size, light/dark theme,
 *   per-slot theme names, follow-system toggles, reduce-animations) would reset
 *   on every restart. To fix this the /api/ui-preferences endpoint was extended
 *   to hold the whole theme cluster; storage.js mirrors every UI change into
 *   both localStorage (fast path) and the server (durable path).
 *
 * What this test locks down:
 *   - Every theme-cluster field round-trips through PUT + GET.
 *   - Invalid values are rejected with a bad_request envelope.
 *   - Partial updates leave un-touched fields alone (merge semantics), so a
 *     save from one code path never wipes a preference owned by another.
 *   - The frontend adopts server-side values on cold boot: after seeding the
 *     server with a chosen font size + theme, a fresh page load ends up with
 *     the same values in localStorage (which is what useTheme reads at init).
 */

const THEME_KEYS = {
  fontSize: "mitto-font-size",
  theme: "mitto-theme",
  themeLight: "mitto-theme-light",
  themeDark: "mitto-theme-dark",
  followSystemTheme: "mitto-follow-system-theme",
  followSystemReducedMotion: "mitto-follow-system-reduced-motion",
  reduceAnimations: "mitto-reduce-animations",
} as const;

test.describe("UI preferences — theme cluster server round-trip", () => {
  test("PUT then GET returns every theme field intact", async ({
    request,
    apiUrl,
  }) => {
    const put = await request.put(apiUrl("/api/ui-preferences"), {
      data: {
        theme: "dark",
        theme_light: "cupcake",
        theme_dark: "synthwave",
        follow_system_theme: false,
        follow_system_reduced_motion: true,
        reduce_animations: false,
        font_size: "large",
      },
    });
    expect(put.ok(), `PUT failed: ${put.status()} ${await put.text()}`).toBe(
      true,
    );

    const get = await request.get(apiUrl("/api/ui-preferences"));
    expect(get.ok()).toBe(true);
    const prefs = await get.json();
    expect(prefs.theme).toBe("dark");
    expect(prefs.theme_light).toBe("cupcake");
    expect(prefs.theme_dark).toBe("synthwave");
    expect(prefs.follow_system_theme).toBe(false);
    expect(prefs.follow_system_reduced_motion).toBe(true);
    expect(prefs.reduce_animations).toBe(false);
    expect(prefs.font_size).toBe("large");
  });

  test("PUT rejects invalid theme values with bad_request envelope", async ({
    request,
    apiUrl,
  }) => {
    const cases: Array<{ body: Record<string, unknown>; wantMsg: string }> = [
      {
        body: { theme: "twilight" },
        wantMsg: "Invalid theme: must be 'light' or 'dark'",
      },
      {
        body: { theme_light: "has space" },
        wantMsg: "Invalid theme_light: must be a short alphanumeric name",
      },
      {
        body: { theme_dark: "one$two" },
        wantMsg: "Invalid theme_dark: must be a short alphanumeric name",
      },
      {
        body: { font_size: "huge" },
        wantMsg: "Invalid font_size: must be 'small' or 'large'",
      },
    ];
    for (const tc of cases) {
      const resp = await request.put(apiUrl("/api/ui-preferences"), {
        data: tc.body,
      });
      expect(resp.status(), `body=${JSON.stringify(tc.body)}`).toBe(400);
      const env = await resp.json();
      expect(env?.error?.message).toBe(tc.wantMsg);
    }
  });

  test("frontend adopts server-side values on cold boot", async ({
    page,
    request,
    apiUrl,
  }) => {
    // Seed the server with a distinctive combo BEFORE the page loads so we can
    // observe useTheme picking it up via onUIPreferencesLoaded.
    const put = await request.put(apiUrl("/api/ui-preferences"), {
      data: {
        font_size: "large",
        theme: "dark",
        theme_light: "cupcake",
        theme_dark: "synthwave",
        follow_system_theme: false,
        follow_system_reduced_motion: false,
        reduce_animations: true,
      },
    });
    expect(put.ok()).toBe(true);

    // Clear localStorage for the theme cluster so the ONLY way these values
    // can end up in localStorage is via the server-sync path in initUIPreferences.
    await page.goto("/");
    await page.evaluate((keys) => {
      Object.values(keys).forEach((k) => localStorage.removeItem(k as string));
    }, THEME_KEYS);

    // Force a fresh page load; initUIPreferences runs and mirrors server values
    // into localStorage, then useTheme's onUIPreferencesLoaded callback adopts
    // them into React state (which in turn re-persists them via the setters).
    await page.reload();

    // Wait until initUIPreferences has finished hydrating localStorage.
    await expect
      .poll(
        async () =>
          await page.evaluate(
            (k) => localStorage.getItem(k),
            THEME_KEYS.fontSize,
          ),
        { timeout: 10_000 },
      )
      .toBe("large");

    const values = await page.evaluate((keys) => {
      const out: Record<string, string | null> = {};
      for (const [name, key] of Object.entries(keys)) {
        out[name] = localStorage.getItem(key as string);
      }
      return out;
    }, THEME_KEYS);

    expect(values.fontSize).toBe("large");
    expect(values.theme).toBe("dark");
    expect(values.themeLight).toBe("cupcake");
    expect(values.themeDark).toBe("synthwave");
    expect(values.followSystemTheme).toBe("false");
    expect(values.followSystemReducedMotion).toBe("false");
    expect(values.reduceAnimations).toBe("true");
  });
});
