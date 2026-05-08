# Conversation Settings

Configure conversation-level behavior: auto-approve, auto-archive, auto-delete, and external images.

For message processing (prepend/append text, external commands, background AI tasks), see **[Processors](processors.md)**.

## Configuration in the UI

Conversation settings are managed in **Settings → Conversations**:

![Settings — Conversations tab](screenshots/02-settings-conversations.png)

From this tab you can configure:

- **Auto-approve tool calls** — Skip confirmation prompts for agent actions
- **Auto-archive conversations** — Archive idle conversations after a set time
- **Auto-delete conversations** — Remove archived conversations after a set time

---

## YAML Configuration

Conversation settings live under the `conversations` key in `~/.mittorc` or `settings.json`:

```yaml
conversations:
  auto_approve: false        # Auto-approve agent tool calls (default: false)
  auto_archive: 0            # Auto-archive after N minutes of inactivity (0 = disabled)
  auto_delete: 0             # Auto-delete archived conversations after N minutes (0 = disabled)
  external_images:
    enabled: false            # Allow external HTTPS images in responses (default: false)
```

### Inline Processors

You can also define simple text-mode processors inline under `conversations.processing`. These are merged with standalone processor files — see **[Processors → Inline Processors](processors.md#inline-processors-in-mittorc)** for details.

## External Images

By default, Mitto blocks external images in AI responses for privacy reasons. When an AI response includes an image from an external URL like `![photo](https://example.com/image.png)`, the browser's Content Security Policy (CSP) prevents loading it.

This is intentional because external images can be used for tracking:

- When you view a message containing an external image, your browser requests that image from the external server
- This reveals your IP address and when you viewed the message

### Enabling External Images

If you want to allow external images (e.g., when working with AI that generates image links), you can enable them:

**Via Settings UI:**

1. Open Settings (⚙️ button)
2. Go to the **UI** tab
3. Under **Advanced**, enable **Allow External Images**
4. Save your settings and restart Mitto for the change to take effect

**Via Configuration:**

```yaml
conversations:
  external_images:
    enabled: true # Allow external HTTPS images (default: false)
```

### Security Considerations

When external images are enabled:

- Only HTTPS images are allowed (not HTTP)
- Data URLs and same-origin images are always allowed regardless of this setting
- Your IP address may be exposed to external image servers
- External servers can track when you view messages

**Recommendation:** Keep external images disabled unless you specifically need them.

## Related Documentation

- [Processors](processors.md) - Message transformation (text, command, prompt modes)
- [User Data](user-data.md) - Custom metadata for conversations
- [Workspace Configuration](workspace.md) - Project-specific `.mittorc` files
- [Configuration Overview](overview.md) - Global configuration options
