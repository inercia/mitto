FROM alpine:latest

RUN apk add --no-cache curl ca-certificates bash nodejs npm

# Install Claude Code CLI globally (provides the `claude` binary)
RUN npm install -g @anthropic-ai/claude-code

# Fake gemini stub (status.sh checks for this binary)
RUN printf '#!/bin/sh\necho "gemini-fake 0.0.1"\n' > /usr/local/bin/gemini \
    && chmod +x /usr/local/bin/gemini

# Non-root user (mirrors real usage)
RUN adduser -D -s /bin/bash mitto

# Cross-compiled binaries from host build (staged in tests/smoke/.build/)
# Must be copied as root (before USER mitto) since /usr/local/bin is root-owned
COPY --chmod=755 tests/smoke/.build/mitto           /usr/local/bin/mitto
COPY --chmod=755 tests/smoke/.build/mock-acp-server /usr/local/bin/mock-acp-server

USER mitto
WORKDIR /home/mitto

# Fake config files that agent status.sh scripts check for
RUN mkdir -p /home/mitto/.claude /home/mitto/.gemini \
    && echo '{}' > /home/mitto/.claude/settings.json \
    && echo '{}' > /home/mitto/.gemini/settings.json

# Workspace directories (required for Playwright workspace tests)
RUN mkdir -p \
    /home/mitto/workspaces/project-alpha \
    /home/mitto/workspaces/project-beta \
    /home/mitto/workspaces/empty-project \
    /home/mitto/workspaces/ws-cmd \
    /home/mitto/workspaces/ws-internal \
    /home/mitto/workspaces/ws-web \
    /home/mitto/workspaces/ws-docs \
    && for d in project-alpha project-beta empty-project ws-cmd ws-internal ws-web ws-docs; do \
         echo "# $d" > /home/mitto/workspaces/$d/README.md; \
       done

# Test fixtures (mock ACP responses + workspace)
COPY --chown=mitto tests/fixtures/responses/           /home/mitto/fixtures/responses/
COPY --chown=mitto tests/fixtures/workspaces/          /home/mitto/fixtures/workspaces/

# Smoke test and entrypoint scripts
COPY --chown=mitto tests/smoke/smoke-test.sh           /home/mitto/smoke-test.sh
COPY --chown=mitto tests/smoke/entrypoint.sh           /home/mitto/entrypoint.sh
RUN chmod +x /home/mitto/smoke-test.sh /home/mitto/entrypoint.sh

ENV MITTO_DIR=/home/mitto/.mitto
EXPOSE 8089

HEALTHCHECK --interval=3s --timeout=2s --start-period=10s --retries=5 \
    CMD curl -sf http://127.0.0.1:8089/mitto/api/health || exit 1

# Use CMD so `docker compose run mitto /home/mitto/smoke-test.sh` can override it
CMD ["/home/mitto/entrypoint.sh"]
