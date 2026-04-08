---
name: "Customize Mitto here"
description: "Set up .mitto directory with customized prompts for this project"
group: "Agents & Mitto"
backgroundColor: "#B3E5FC"
---

Set up Mitto configuration for this project: create `.mitto` directory structure,
analyze the project, and create customized prompt overrides tailored to its
technologies and workflows.

Read multiple configuration files in parallel (Makefile, package.json, go.mod, CI configs).

## Step 1: Create Directory Structure

```
.mitto/
└── prompts/
```

Create `.mitto/` and `.mitto/prompts/` if they don't exist.

## Step 2: Create `.mittorc`

Create `.mittorc` in the project root:

```yaml
# Mitto workspace configuration
# See: https://github.com/inercia/mitto/blob/main/docs/config/prompts.md

prompts_dirs:
  - ".mitto/prompts"
```

## Step 3: Analyze the Project

Investigate:

- **Build & Development**: Build system, package manager, dev commands, custom scripts. Check `Makefile`, `package.json`, `go.mod`, etc.
- **Testing**: Frameworks, test types (unit/integration/e2e), specific commands. Check Makefile targets, package.json scripts.
- **Code Quality**: Linters, formatters (`golangci-lint`, `gofmt`, `prettier`, etc.)
- **CI/CD**: Deployment scripts, CI configuration (`.github/workflows/`, `.gitlab-ci.yml`)
- **Project-Specific Workflows**: Domain-specific commands, custom Makefile targets

## Step 4: Examine Builtin Prompts

Review builtin prompts in `MITTO_DIR/prompts/builtin/` (typically `~/Library/Application Support/Mitto/prompts/builtin/` on macOS):
`run-tests.md`, `add-tests.md`, `fix-ci.md`, `cleanup-code.md`, `check-ci.md`

## Step 5: Create Customized Prompt Overrides

Create project-specific versions in `.mitto/prompts/` with the **same name** as the builtin prompts they override.

### Required Customizations

#### 1. `run-tests.md`

```markdown
---
name: "Run tests"
description: "Run the test suite and report results"
backgroundColor: "#FFE0B2"
---

Run the project's test suite using the Makefile targets.

### 1. Run Tests

**Go Unit Tests:** `make test-go`
**JavaScript Unit Tests:** `make test-js`
**Integration Tests:** `make test-integration`
**UI Tests (Playwright):** `make test-ui`
**All Tests:** `make test-all`

### 2. Analyze Results

If tests pass, report success.

If tests fail:
- Simple failures: fix immediately, re-run to verify
- Complex failures: report for manual review

### 3. Summary Table

| Test Suite | Command | Passed | Failed | Status |
|------------|---------|--------|--------|--------|
| Go Unit | `make test-go` | 45 | 0 | ✅ |
| JS Unit | `make test-js` | 12 | 1 | ❌ |
| Integration | `make test-integration` | 8 | 0 | ✅ |
| UI (Playwright) | `make test-ui` | 15 | 0 | ✅ |
| **Total** | | **80** | **1** | ❌ |



### 4. Unresolved Failures

Per failure: **Test Suite**, **Test**, **Error**, **Cause**, **Suggested fix**


```

#### 2. `add-tests.md`

```markdown
---
name: "Add tests"
description: "Write comprehensive tests for new or modified code"
backgroundColor: "#FFE0B2"
---

Read the modified code and existing test files to understand testing conventions.

Write comprehensive tests following this project's conventions.

### Frameworks

**Go:** Table-driven tests with `t.Run()`, mock external deps. Tests in same package.
**JavaScript:** Jest with jsdom. Test files: `*.test.js` in `web/static/`.
**Integration:** Mock ACP server from `tests/integration/mock_acp_server.go`, `httptest`.
**UI:** Playwright with TypeScript, page object pattern. Tests in `tests/ui/`.

### Coverage

1. Happy path
2. Edge cases (empty inputs, boundaries)
3. Error cases (invalid inputs, missing data)
4. Concurrency (if applicable)
5. Integration with dependencies

### Test Structure

**Go (Table-Driven):**
```go
func TestFunctionName(t *testing.T) {
    tests := []struct {
        name     string
        input    string
        expected string
        wantErr  bool
    }{
        {"happy path", "input", "output", false},
        {"error case", "bad", "", true},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            result, err := FunctionName(tt.input)
            if (err != nil) != tt.wantErr {
                t.Errorf("unexpected error: %v", err)
            }
            if result != tt.expected {
                t.Errorf("got %v, want %v", result, tt.expected)
            }
        })
    }
}
```

**JavaScript (Jest):**
```javascript
describe('functionName', () => {
  it('should handle happy path', () => {
    expect(functionName('input')).toBe('output');
  });
  it('should handle error case', () => {
    expect(() => functionName('bad')).toThrow();
  });
});
```

### After Writing

Run appropriate suite: `make test-go`, `make test-js`, `make test-integration`, or `make test-ui`.

```

#### 3. `fix-ci.md`

```markdown
---
name: "Fix CI"
description: "Diagnose and fix CI pipeline failures"
backgroundColor: "#B2DFDB"
---

Check CI status and read failure logs before making changes.

Diagnose and fix GitHub Actions CI failures for the current branch.

Only fix CI-failing issues. Keep changes minimal.


### 1. Check GitHub CLI

```bash
gh auth status
```

### 2. Get CI Status

```bash
git branch --show-current
gh run list --branch $(git branch --show-current) --limit 5
```

If CI is passing, report success and stop.

### 3. Diagnose Failures

**Workflows:**
- **Tests** (`.github/workflows/tests.yml`): `make test-ci`, Go formatting, linting
- **Release** (`.github/workflows/release.yml`): Multi-platform builds, GitHub releases
- **Homebrew** (`.github/workflows/homebrew.yml`): Formula testing

```bash
gh run view <run-id> --log-failed
```

### 4. Common Patterns

- **Go tests**: Race conditions (`go test -race`), missing mocks, incomplete table-driven tests
- **JS tests**: Missing Jest mocks, jsdom setup, async timing
- **Playwright**: Selector changes, stale fixtures, timing (use proper waits)
- **Formatting/Linting**: `make fmt`, `make lint`, `npx prettier --write .`
- **Build**: Missing deps (`make deps-go deps-js`), Go version mismatch, import errors

### 5. Fix and Verify

1. Implement fix
2. Run `make test-ci` locally
3. Commit and push

### 6. Monitor

```bash
gh run watch
```

- Run `make test-ci` locally before pushing
- Get user approval before modifying workflow files or dependencies
- Group related fixes in a single commit

```

#### 4. `cleanup-code.md`

```markdown
---
name: "Cleanup Code"
description: "Remove dead code, unused imports, and outdated documentation"
backgroundColor: "#E8F5E9"
---

Read relevant code and search for references before proposing cleanup.
Read multiple files in parallel.

Analyze for cleanup opportunities. Propose a plan and wait for approval.

### 1. Analyze

**Unused Imports:**
- Go: `goimports -l .`
- JS: Check ESLint output for `web/static/` files

**Dead Code:**
- Go: `golangci-lint run --enable=unused,deadcode`
- Look for: unexported functions never called, exported functions with no references, unused fields/constants
- JS: Review `web/static/utils/lib.js`, unused component functions/hooks

**Commented-Out Code:**
```bash
grep -r "^\s*//" internal/ cmd/ web/static/ | grep -v "^\s*// " | head -20
```

**Outdated Docs:** Check `docs/`, code comments, `.augment/rules/`
**Obsolete Tests:** Unused helpers in `*_test.go`, fixtures in `tests/integration/`, Playwright page objects

### 2. Propose Plan



| Priority | Category | Location | Description | Risk | Effort |
|----------|----------|----------|-------------|------|--------|
| 1 | Dead Code | `internal/pkg/file.go` | Remove unused `oldHelper()` | Low | Small |



### 3. Wait for Approval

Options: **Approve all**, **Approve selected**, **Investigate**, **Cancel**

### 4. Execute

Per item: make change, run checks (`make test-go`/`make test-js`/`make fmt lint`), report.

### 5. Summary

Report changes made, verification status, skipped items.

## Guidelines

- Propose before removing; wait for approval
- Run tests after changes: `make test-go` or `make test-js`
- Run `make fmt` for Go code
- Be conservative with exported APIs
- Update related docs when removing code

```

### Optional Customizations

Consider also customizing: `check-ci.md` (GitHub Actions status), `document-code.md` (project's `docs/devel/` structure).

### Prompt File Format

```markdown
---
name: "Prompt Name"
description: "What this prompt does"
backgroundColor: "#HEXCOLOR"
---

Prompt content here...
```

**Colors:** Testing: `#FFE0B2`, CI/Build: `#B2DFDB`, Code quality: `#E8F5E9`, Docs: `#F3E5F5`, General: `#B3E5FC`

## Step 6: Summary

After setup:
1. ✅ `.mitto/` and `.mitto/prompts/` created
2. ✅ `.mittorc` configured
3. ✅ Project analyzed
4. ✅ Builtin prompts examined
5. ✅ Customized overrides created: `run-tests.md`, `add-tests.md`, `fix-ci.md`, `cleanup-code.md`

### How Overrides Work

Mitto searches: `.mitto/prompts/` first, then `MITTO_DIR/prompts/builtin/`. Same-name files override builtins.
