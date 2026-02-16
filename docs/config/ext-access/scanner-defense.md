
# Scanner Defense

When external access is enabled (`external_port >= 0`), Mitto automatically activates
**Scanner Defense** on the external listener to block malicious IPs at the TCP connection
level. This protects against automated vulnerability scanners and brute-force attacks.

> **Note:** Scanner defense only applies to the **external listener** (0.0.0.0). The
> localhost listener (127.0.0.1) is not affected, ensuring local development is never
> interrupted by defense mechanisms.

**How it works:**

1. **Connection-level blocking** - Blocked IPs are rejected at the external listener before HTTP parsing
2. **Rate limiting** - IPs exceeding request thresholds are blocked
3. **Error rate analysis** - IPs with high error rates (e.g., 90%+ 4xx/5xx responses) are blocked
4. **Suspicious path detection** - IPs probing scanner paths (`/.env`, `/.git/`, `/wp-admin`, etc.) are blocked
5. **Persistent blocklist** - Blocked IPs remain blocked across server restarts

**Default thresholds:**

| Setting          | Default     | Description                              |
| ---------------- | ----------- | ---------------------------------------- |
| Rate limit       | 100 req/min | Max requests per minute before blocking  |
| Error rate       | 90%         | Error rate threshold (with 10+ requests) |
| Suspicious paths | 5 hits      | Suspicious path hits before blocking     |
| Block duration   | 24 hours    | How long IPs remain blocked              |

**Customization:**

```yaml
web:
  external_port: 8443

  security:
    scanner_defense:
      # Enabled automatically when external_port >= 0
      # Set to false to disable:
      enabled: true

      # Override defaults:
      rate_limit: 50 # Max requests per window
      rate_window_seconds: 60 # Rate limit window (seconds)
      error_rate_threshold: 0.8 # 80% error rate triggers block
      min_requests: 10 # Min requests before error analysis
      suspicious_path_threshold: 3 # Suspicious path hits before block
      block_duration_seconds: 86400 # Block for 24 hours

      # Additional whitelisted IPs (localhost is always whitelisted)
      whitelist:
        - 10.0.0.0/8
        - 192.168.0.0/16
```

**Disable Scanner Defense:**

```yaml
web:
  external_port: 8443
  security:
    scanner_defense:
      enabled: false
```
