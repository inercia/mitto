import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { expect, jest, test } from "../utils/testing/testGlobals.js";

if (process.env.MITTO_ATTACHMENT_IMAGE_TEST_CHILD === "1") {
  const preact = await import("../vendor/preact.js");
  const hooks = await import("../vendor/preact-hooks.js");
  const htm = (await import("../vendor/htm.js")).default;
  const previousPreact = window.preact;
  const html = htm.bind(preact.h);
  window.preact = { ...preact, ...hooks, html };
  const { AttachmentImage } = await import(
    "./AttachmentImage.js?historical-image-mounted-tests"
  );
  window.preact = previousPreact;

  function mount(image, container = document.createElement("div")) {
    if (!container.isConnected) document.body.appendChild(container);
    preact.render(html`<${AttachmentImage} image=${image} />`, container);
    return container;
  }

  async function fail(container) {
    container.querySelector("img").dispatchEvent(new Event("error"));
    await new Promise((resolve) => setTimeout(resolve, 0));
  }

  function unmount(container) {
    preact.render(null, container);
    container.remove();
  }

  test("renders a healthy image with alt text and click-to-open behavior", () => {
    const open = window.open;
    window.open = jest.fn();
    const image = { id: "healthy", url: "/session-a/healthy.png", name: "Photo" };
    const container = mount(image);
    try {
      const img = container.querySelector("img");
      expect(img.getAttribute("src")).toBe(image.url);
      expect(img.getAttribute("alt")).toBe("Photo");
      img.click();
      expect(window.open).toHaveBeenCalledWith(image.url, "_blank");
    } finally {
      window.open = open;
      unmount(container);
    }
  });

  test("failure is accessible and retry makes one new image attempt", async () => {
    const image = { id: "retryable", url: "/session-a/retryable.png", name: "Receipt" };
    const originalSetAttribute = Element.prototype.setAttribute;
    let imageRequests = 0;
    Element.prototype.setAttribute = function (name, value) {
      if (this.tagName === "IMG" && name === "src" && value === image.url) {
        imageRequests++;
      }
      return originalSetAttribute.call(this, name, value);
    };
    const container = mount(image);
    try {
      expect(imageRequests).toBe(1);
      await fail(container);
      expect(container.textContent).toContain("Image unavailable");
      expect(container.textContent).toContain("Receipt");
      expect(container.textContent).toContain("The attachment could not be loaded.");
      expect(container.querySelector("img")).toBeNull();
      expect(container.querySelector("[role=status]")).not.toBeNull();
      mount(image, container);
      expect(imageRequests).toBe(1);
      container.querySelector("button").click();
      await new Promise((resolve) => setTimeout(resolve, 0));
      expect(container.querySelector("img").getAttribute("src")).toBe(image.url);
      expect(imageRequests).toBe(2);
    } finally {
      Element.prototype.setAttribute = originalSetAttribute;
      unmount(container);
    }
  });

  test("failed URL stays suppressed after rerender and unmount/remount", async () => {
    const image = { id: "stable", url: "/session-a/stable.png", name: "Stable" };
    const container = mount(image);
    await fail(container);
    mount(image, container);
    expect(container.querySelector("img")).toBeNull();
    unmount(container);

    const remounted = mount(image);
    try {
      expect(remounted.querySelector("img")).toBeNull();
    } finally {
      unmount(remounted);
    }
  });

  test("same ID in different session URLs has independent failure state", async () => {
    const a = { id: "shared-id", url: "/session-a/shared.png", name: "A" };
    const b = { id: "shared-id", url: "/session-b/shared.png", name: "B" };
    const failed = mount(a);
    await fail(failed);
    const otherSession = mount(b);
    try {
      expect(failed.querySelector("img")).toBeNull();
      expect(otherSession.querySelector("img")).not.toBeNull();
    } finally {
      unmount(failed);
      unmount(otherSession);
    }
  });

  test("changing URL uses the new URL state, and cache evicts its oldest entry", async () => {
    const first = { id: "same", url: "/session-a/change-old.png", name: "Old" };
    const second = { id: "same", url: "/session-a/change-new.png", name: "New" };
    const container = mount(first);
    await fail(container);
    mount(second, container);
    expect(container.querySelector("img").getAttribute("src")).toBe(second.url);
    unmount(container);

    const oldest = { id: "evict-0", url: "/cache-eviction/0.png" };
    const oldestContainer = mount(oldest);
    await fail(oldestContainer);
    unmount(oldestContainer);
    for (let i = 1; i <= 512; i++) {
      const item = { id: `evict-${i}`, url: `/cache-eviction/${i}.png` };
      const node = mount(item);
      await fail(node);
      unmount(node);
    }
    const afterEviction = mount(oldest);
    try {
      expect(afterEviction.querySelector("img")).not.toBeNull();
    } finally {
      unmount(afterEviction);
    }
  });
} else {
  test("AttachmentImage mounted behavior passes in an isolated happy-dom process", () => {
    const result = spawnSync(
      process.execPath,
      ["test", fileURLToPath(import.meta.url)],
      {
        encoding: "utf8",
        env: { ...process.env, MITTO_ATTACHMENT_IMAGE_TEST_CHILD: "1" },
        timeout: 30_000,
      },
    );
    if (result.status !== 0) {
      throw new Error(
        `AttachmentImage mounted tests failed:\n${result.stdout}\n${result.stderr}`,
      );
    }
  });
}
