// A failed image request is remembered only for this page lifetime. Keeping
// the cache URL-scoped prevents failures in one session affecting another.
const failedImageURLs = new Map();
const MAX_FAILED_IMAGE_URLS = 512;
const { html, useState } = window.preact;

function rememberFailure(url) {
  failedImageURLs.delete(url);
  failedImageURLs.set(url, true);
  if (failedImageURLs.size > MAX_FAILED_IMAGE_URLS) {
    failedImageURLs.delete(failedImageURLs.keys().next().value);
  }
}

export function AttachmentImage({ image }) {
  const [failure, setFailure] = useState(() => ({
    url: image.url,
    failed: failedImageURLs.has(image.url),
  }));
  const failed = failure.url === image.url
    ? failure.failed || failedImageURLs.has(image.url)
    : failedImageURLs.has(image.url);

  if (failed) {
    return html`<div class="flex flex-col items-start gap-1 p-2" role="status">
      <span class="font-medium">Image unavailable</span>
      <span class="text-sm">${image.name || image.id}</span>
      <span class="text-sm">The attachment could not be loaded.</span>
      <button
        type="button"
        class="btn btn-ghost btn-xs"
        onClick=${() => {
          failedImageURLs.delete(image.url);
          setFailure({ url: image.url, failed: false });
        }}
      >Retry image</button>
    </div>`;
  }

  return html`<div class="relative group">
    <img
      src=${image.url}
      alt=${image.name || "Attached image"}
      class="max-w-[200px] max-h-[150px] rounded-lg object-cover cursor-pointer hover:opacity-90 transition-opacity"
      onClick=${() => window.open(image.url, "_blank")}
      onError=${() => {
        rememberFailure(image.url);
        setFailure({ url: image.url, failed: true });
      }}
    />
  </div>`;
}
