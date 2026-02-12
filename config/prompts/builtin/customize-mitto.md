---
name: "Customize Mitto here"
description: "Set up .mitto directory with customized prompts for this project"
backgroundColor: "#B3E5FC"
---

Set up the Mitto configuration for this project by creating the `.mitto` directory structure,
analyzing the project, and creating customized versions of builtin prompts that are tailored
to this project's specific technologies and workflows.

## Step 1: Create Directory Structure

Create the following directory structure in the project root:

```
.mitto/
└── prompts/
```

1. Create the `.mitto` directory if it doesn't exist
2. Create the `.mitto/prompts` subdirectory for project-specific prompts

## Step 2: Create the `.mittorc` File

Create a `.mittorc` file in the project root with the following content:

```yaml
# Mitto workspace configuration
# See: https://github.com/inercia/mitto/blob/main/docs/config/prompts.md

prompts_dirs:
  - ".mitto/prompts"
```

This tells Mitto to look for prompt files in the `.mitto/prompts` directory.

## Step 3: Analyze the Project

Before creating any prompts, analyze the project to understand:

### Build & Development
- What build system or package manager is used? (npm, yarn, cargo, go, make, etc.)
- What are the common development commands? (build, dev server, watch mode)
- Are there any custom scripts defined?
- Check `Makefile`, `package.json`, `go.mod`, etc.

### Testing
- What testing frameworks are used?
  - Go: `go test`, table-driven tests
  - JavaScript: Jest, Playwright
  - Integration tests: Mock servers, in-process testing
- How are tests run? (unit tests, integration tests, e2e tests)
- Are there specific test patterns or commands?
- Check `Makefile` targets, `package.json` scripts

### Code Quality
- What linters or formatters are configured?
- How is code quality checked? (lint, format, typecheck)
- Check for `golangci-lint`, `gofmt`, `prettier`, etc.

### Deployment & CI/CD
- Are there deployment scripts or commands?
- Is there a CI/CD configuration?
- Check `.github/workflows/`, `.gitlab-ci.yml`, etc.

### Project-Specific Workflows
- Are there any domain-specific commands or workflows?
- Are there common tasks specific to this project type?
- Check for custom Makefile targets, scripts, etc.

## Step 4: Examine Builtin Prompts

Before creating custom prompts, examine the builtin prompts in `MITTO_DIR/prompts/builtin/`
(typically `~/Library/Application Support/Mitto/prompts/builtin/` on macOS).

Key builtin prompts to review:
- `run-tests.md` - Generic test running instructions
- `add-tests.md` - Generic test writing instructions
- `fix-ci.md` - Generic CI fixing instructions
- `cleanup-code.md` - Generic code cleanup instructions
- `check-ci.md` - Generic CI status checking

## Step 5: Create Customized Prompt Overrides

Create customized versions of builtin prompts in `.mitto/prompts/` that override the generic
versions with project-specific instructions. These prompts should have the **same name** as
the builtin prompts they override.

### Required Customizations

Create the following customized prompts based on this project's technology stack:

#### 1. `run-tests.md` - Customized Test Runner

**Why customize**: This project uses multiple test frameworks (Go, Jest, Playwright) with
specific Makefile targets.

```markdown
---
name: "Run tests"
description: "Run the test suite and report results"
backgroundColor: "#FFE0B2"
---

Run the project's test suite using the Makefile targets.

### 1. Run Tests

This project uses multiple test frameworks:

**Go Unit Tests:**
```bash
make test-go
```
Runs all Go unit tests in `internal/` and `cmd/` packages.

**JavaScript Unit Tests:**
```bash
make test-js
```
Runs Jest tests for the web interface (`web/static/**/*.test.js`).

**Integration Tests:**
```bash
make test-integration
```
Runs Go integration tests with mock ACP server.

**UI Tests (Playwright):**
```bash
make test-ui
```
Runs Playwright end-to-end tests.

**All Tests:**
```bash
make test-all
```
Runs all test suites (Go, JS, integration, UI).

### 2. Analyze Results

If tests pass:
- Report success with a brief summary

If tests fail:
- For simple failures (typos, missing imports, obvious fixes):
  - Fix the issue immediately
  - Re-run the specific test suite to verify the fix
  - Repeat until tests pass or failures are complex
- For complex failures:
  - Report them for manual review

### 3. Summary Table

Present a results table:

| Test Suite | Command | Passed | Failed | Status |
|------------|---------|--------|--------|--------|
| Go Unit | `make test-go` | 45 | 0 | ✅ |
| JS Unit | `make test-js` | 12 | 1 | ❌ |
| Integration | `make test-integration` | 8 | 0 | ✅ |
| UI (Playwright) | `make test-ui` | 15 | 0 | ✅ |
| **Total** | | **80** | **1** | ❌ |

### 4. If Failures Remain

For each unresolved failure:
- **Test Suite**: Which test suite failed (Go/JS/Integration/UI)
- **Test**: Name of the failing test
- **Error**: The error message
- **Cause**: Brief analysis of why it failed
- **Suggested fix**: What needs to change
```

#### 2. `add-tests.md` - Customized Test Writing

**Why customize**: This project has specific testing conventions for Go (table-driven tests)
and JavaScript (Jest with jsdom).

```markdown
---
name: "Add tests"
description: "Write comprehensive tests for new or modified code"
backgroundColor: "#FFE0B2"
---

Write comprehensive tests following this project's testing conventions.

### Testing Frameworks

**Go Tests:**
- Use table-driven tests for multiple test cases
- Follow the pattern in existing `*_test.go` files
- Use `t.Run()` for subtests
- Mock external dependencies (ACP server, file system)
- Test files go in the same package as the code (e.g., `session_test.go` for `session.go`)

**JavaScript Tests:**
- Use Jest with jsdom environment
- Test files: `*.test.js` in `web/static/`
- Mock browser globals (localStorage, WebSocket)
- Test pure functions in `lib.js`

**Integration Tests:**
- Use mock ACP server from `tests/integration/mock_acp_server.go`
- Test in-process with `httptest`
- Test files in `tests/integration/`

**UI Tests:**
- Use Playwright with TypeScript config
- Test files in `tests/ui/`
- Use page object pattern for reusable selectors

### Include tests for:

1. **Happy path**: Normal expected usage
2. **Edge cases**: Empty inputs, boundary values, maximum sizes
3. **Error cases**: Invalid inputs, missing data, permission errors
4. **Concurrency**: Race conditions, deadlocks (if applicable)
5. **Integration**: Interaction with dependencies

### Test structure:

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
    const result = functionName('input');
    expect(result).toBe('output');
  });

  it('should handle error case', () => {
    expect(() => functionName('bad')).toThrow();
  });
});
```

### After writing:

- Run the appropriate test suite:
  - `make test-go` for Go tests
  - `make test-js` for JavaScript tests
  - `make test-integration` for integration tests
  - `make test-ui` for UI tests
- Verify all tests pass
- Check that new code is covered
```

#### 3. `fix-ci.md` - Customized CI Fixing

**Why customize**: This project uses GitHub Actions with specific workflows.

```markdown
---
name: "Fix CI"
description: "Diagnose and fix CI pipeline failures"
backgroundColor: "#B2DFDB"
---

Diagnose and fix GitHub Actions CI failures for the current branch.

### 1. Check GitHub CLI

Verify `gh` CLI is installed and authenticated:
```bash
gh auth status
```

If not authenticated, run:
```bash
gh auth login
```

### 2. Get Current Branch and CI Status

```bash
# Get current branch
git branch --show-current

# Check workflow runs
gh run list --branch $(git branch --show-current) --limit 5
```

If CI is passing, report success and stop.

### 3. Diagnose Failures

This project has three main workflows:

**Tests Workflow (`.github/workflows/tests.yml`):**
- Runs on every push and PR
- Executes: `make test-ci` (Go, JS, integration, UI tests)
- Checks: Go formatting, linting

**Release Workflow (`.github/workflows/release.yml`):**
- Runs on tags
- Builds binaries for multiple platforms
- Creates GitHub releases

**Homebrew Workflow (`.github/workflows/homebrew.yml`):**
- Tests Homebrew formula
- Runs on changes to formula files

Get the failed run logs:
```bash
gh run view <run-id> --log-failed
```

### 4. Common Failure Patterns

**Go Test Failures:**
- Check for race conditions: `go test -race`
- Check for missing mocks or test fixtures
- Verify table-driven test cases are complete

**JavaScript Test Failures:**
- Check for missing Jest mocks
- Verify jsdom environment is set up correctly
- Check for async timing issues

**Playwright Test Failures:**
- Check for selector changes in UI
- Verify test fixtures are up to date
- Check for timing issues (use proper waits)

**Formatting/Linting:**
- Run `make fmt` to fix Go formatting
- Run `make lint` to check for issues
- Run `npx prettier --write .` for JS formatting

**Build Failures:**
- Check for missing dependencies: `make deps-go deps-js`
- Verify Go version matches `go.mod`
- Check for import errors

### 5. Fix Issues

For each identified issue:
1. **Implement the fix** in the codebase
2. **Explain the change** and why it resolves the issue
3. **Verify locally**:
   - Run `make test-ci` to run all CI checks locally
   - Run specific test suite if needed
   - Run `make fmt lint` for code quality

### 6. Commit and Push

After fixes are implemented:
```bash
git add .
git commit -m "fix: resolve CI failures"
git push
```

### 7. Monitor CI

```bash
gh run watch
```

The tests workflow typically takes 5-10 minutes to complete.

## Rules

- Always run `make test-ci` locally before pushing
- Never modify workflow files without explicit user approval
- If fixes require dependency changes, ask user for approval first
- Group related fixes in a single commit when possible
```

#### 4. `cleanup-code.md` - Customized Code Cleanup

**Why customize**: This project uses specific Go and JavaScript tools.

```markdown
---
name: "Cleanup Code"
description: "Remove dead code, unused imports, and outdated documentation"
backgroundColor: "#E8F5E9"
---

Analyze the codebase for cleanup opportunities using project-specific tools.

**Do not make changes immediately. Propose a plan first and wait for approval.**

### 1. Analyze the Codebase

**Unused Imports:**

**Go:**
```bash
# goimports will remove unused imports
goimports -l .
```

**JavaScript:**
- Check ESLint output for unused variables
- Review import statements in `web/static/` files

**Dead Code:**

**Go:**
```bash
# Use golangci-lint to find unused code
golangci-lint run --enable=unused,deadcode
```

Look for:
- Unexported functions never called within the package
- Exported functions with no references in the codebase
- Unused struct fields
- Unused constants and variables

**JavaScript:**
- Review functions in `web/static/utils/lib.js`
- Check for unused component functions
- Look for unused hooks

**Commented-Out Code:**

Search for large blocks of commented-out code:
```bash
grep -r "^\s*//" internal/ cmd/ web/static/ | grep -v "^\s*// " | head -20
```

**Outdated Documentation:**

- Check `docs/` for references to deleted features
- Review comments in code for accuracy
- Check `.augment/rules/` for outdated patterns

**Obsolete Test Code:**

- Look for unused test helpers in `*_test.go` files
- Check for unused fixtures in `tests/integration/`
- Review unused Playwright page objects

### 2. Propose Cleanup Plan

Present a prioritized table:

| Priority | Category | Location | Description | Risk | Effort |
|----------|----------|----------|-------------|------|--------|
| 1 | Dead Code | `internal/pkg/file.go` | Remove unused function `oldHelper()` | Low | Small |
| 2 | Imports | `internal/session/store.go` | Remove 3 unused imports | Low | Small |
| 3 | Documentation | `docs/devel/api.md` | Update outdated API references | Low | Medium |

### 3. Wait for Approval

Ask the user to:
- **Approve all** - proceed with all cleanup items
- **Approve selected** - specify which items (by priority number)
- **Investigate** - get more details on specific items
- **Cancel** - abort without making changes

**Do not proceed until the user explicitly approves.**

### 4. Execute Approved Changes

For each approved item:
1. Make the change
2. Run appropriate checks:
   - `make test-go` for Go changes
   - `make test-js` for JavaScript changes
   - `make fmt lint` for code quality
3. Report the result

### 5. Report Summary

```markdown
## Cleanup Summary

### Changes Made
- `internal/pkg/file.go`: Removed unused function `oldHelper()`
- `internal/session/store.go`: Removed 3 unused imports

### Verification
- ✅ Go tests passing (`make test-go`)
- ✅ JS tests passing (`make test-js`)
- ✅ Linter checks passing (`make lint`)
- ✅ Code formatted correctly (`make fmt`)

### Skipped Items
- Item #4: Skipped per user request
```

## Rules

- **Never remove code without proposing first**
- **Always run tests after changes**: `make test-go` or `make test-js`
- **Run formatting**: `make fmt` for Go code
- **Be conservative with exported APIs**: They might be used externally
- **Update related documentation**: Keep docs in sync
```

### Optional Customizations

Consider creating these additional customized prompts if they would be useful:

#### 5. `check-ci.md` - Customized CI Status Check

Customize to show GitHub Actions status for this project's specific workflows.

#### 6. `document-code.md` - Customized Documentation

Customize to follow this project's documentation structure in `docs/devel/`.

### Prompt File Format

All prompts should use this format:

```markdown
---
name: "Prompt Name"
description: "What this prompt does"
backgroundColor: "#HEXCOLOR"
---

Prompt content here...
```

**Color Guidelines:**
- Testing prompts: `#FFE0B2` (orange)
- CI/Build prompts: `#B2DFDB` (teal)
- Code quality prompts: `#E8F5E9` (green)
- Documentation prompts: `#F3E5F5` (purple)
- General prompts: `#B3E5FC` (blue)

## Step 6: Summary

After completing this setup:

1. ✅ `.mitto/` directory created
2. ✅ `.mitto/prompts/` directory created
3. ✅ `.mittorc` file created with `prompts_dirs` configuration
4. ✅ Project analyzed for technology stack and workflows
5. ✅ Builtin prompts examined
6. ✅ Customized prompt overrides created in `.mitto/prompts/`:
   - `run-tests.md` - Customized for Go, Jest, Playwright, Makefile targets
   - `add-tests.md` - Customized for table-driven Go tests and Jest patterns
   - `fix-ci.md` - Customized for GitHub Actions workflows
   - `cleanup-code.md` - Customized for Go and JavaScript tooling
   - (Optional) Additional customized prompts as needed

### How Prompt Overrides Work

When you use a prompt, Mitto searches for it in this order:
1. `.mitto/prompts/` (project-specific overrides)
2. `MITTO_DIR/prompts/builtin/` (builtin prompts)

By creating prompts with the same name in `.mitto/prompts/`, you override the builtin
versions with project-specific instructions.

### Next Steps

You can now use the customized prompts:
- Click "Run tests" to run tests using the project's Makefile targets
- Click "Add tests" to write tests following this project's conventions
- Click "Fix CI" to diagnose and fix GitHub Actions failures
- Click "Cleanup Code" to clean up code using project-specific tools

The prompts are now tailored to this project's specific technology stack and workflows!

