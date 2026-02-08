# Runner System Manual Test Plan

## Test Environment
- **Platform**: macOS (Darwin)
- **Available Runners**: 
  - ✅ exec (always available)
  - ✅ sandbox-exec (macOS native)
  - ❌ firejail (Linux only - will test fallback)
  - ⚠️ docker (installed but daemon not running - will test both scenarios)

## Test Scenarios

### Scenario 1: exec Runner (Baseline)
**Purpose**: Verify no restrictions are applied

**Setup**:
1. Create test workspace with `restricted_runner: "exec"`
2. Start session

**Expected Results**:
- ✅ Session starts successfully
- ✅ Session header shows: `⚠️ exec`
- ✅ Badge is yellow (no restrictions)
- ✅ No toast notification
- ✅ Full file system access
- ✅ Network access works

**Test Commands** (in session):
```bash
# Should all work without restrictions
ls $HOME/.ssh
curl https://example.com
echo "test" > /tmp/test-file.txt
```

---

### Scenario 2: sandbox-exec Runner (macOS)
**Purpose**: Verify macOS sandboxing works correctly

**Setup**:
1. Create test workspace with `restricted_runner: "sandbox-exec"`
2. Configure restrictions in global config or .mittorc:
   ```yaml
   restricted_runners:
     sandbox-exec:
       restrictions:
         allow_networking: true
         allow_read_folders:
           - "$WORKSPACE"
           - "$HOME/.config"
         allow_write_folders:
           - "$WORKSPACE"
         deny_folders:
           - "$HOME/.ssh"
   ```
3. Start session

**Expected Results**:
- ✅ Session starts successfully
- ✅ Session header shows: `🔒 sandbox-exec`
- ✅ Badge is green (restricted)
- ✅ No toast notification (runner is supported)
- ✅ Can read allowed folders
- ✅ Cannot read denied folders
- ✅ Network access works (if allowed)

**Test Commands**:
```bash
# Should work (allowed)
ls $WORKSPACE
cat $HOME/.config/some-file

# Should fail (denied)
ls $HOME/.ssh

# Should work (networking allowed)
curl https://example.com
```

---

### Scenario 3: firejail Runner (Fallback Test)
**Purpose**: Verify fallback to exec when runner is unavailable

**Setup**:
1. Create test workspace with `restricted_runner: "firejail"`
2. Start session

**Expected Results**:
- ✅ Session starts successfully (with fallback)
- ✅ Session header shows: `⚠️ exec`
- ✅ Badge is yellow (no restrictions)
- ✅ Toast notification appears:
  ```
  ⚠️ Runner Not Supported
  Requested: firejail
  Using: exec (no restrictions)
  firejail is only available on Linux
  ```
- ✅ Full file system access (no restrictions)

---

### Scenario 4: docker Runner (Docker Running)
**Purpose**: Verify Docker containerization works

**Prerequisites**: Start Docker daemon

**Setup**:
1. Create test workspace with `restricted_runner: "docker"`
2. Configure Docker restrictions:
   ```yaml
   restricted_runners:
     docker:
       restrictions:
         allow_networking: true
         docker:
           image: "alpine:latest"
           memory_limit: "512m"
           cpu_limit: "1.0"
         allow_read_folders:
           - "$WORKSPACE"
         allow_write_folders:
           - "$WORKSPACE"
   ```
3. Start session

**Expected Results**:
- ✅ Session starts successfully
- ✅ Session header shows: `🔒 docker`
- ✅ Badge is green (restricted)
- ✅ No toast notification
- ✅ Runs in container
- ✅ Workspace mounted correctly
- ✅ Network access works (if allowed)

---

### Scenario 5: docker Runner (Docker Not Running)
**Purpose**: Verify fallback when Docker daemon is unavailable

**Prerequisites**: Stop Docker daemon

**Setup**:
1. Create test workspace with `restricted_runner: "docker"`
2. Start session

**Expected Results**:
- ✅ Session starts successfully (with fallback)
- ✅ Session header shows: `⚠️ exec`
- ✅ Badge is yellow (no restrictions)
- ✅ Toast notification appears:
  ```
  ⚠️ Runner Not Supported
  Requested: docker
  Using: exec (no restrictions)
  [Docker error message]
  ```

---

## Pre-flight Validation Tests

### Test 1: Save Workspace with Unsupported Runner
**Steps**:
1. Open workspace settings
2. Select `firejail` as runner type
3. Click Save

**Expected**:
- ✅ Configuration saves successfully
- ✅ Server logs warning: "firejail is only available on Linux"
- ✅ No error shown to user (warning only)

### Test 2: Save Workspace with Docker (Not Running)
**Steps**:
1. Stop Docker daemon
2. Open workspace settings
3. Select `docker` as runner type
4. Click Save

**Expected**:
- ✅ Configuration saves successfully
- ✅ Server logs warning about Docker availability
- ✅ No error shown to user

---

## UI/UX Tests

### Test 1: Runner Badge Visibility
**Steps**:
1. Create sessions with different runners
2. Switch between sessions

**Expected**:
- ✅ Badge updates when switching sessions
- ✅ Correct icon (🔒 or ⚠️)
- ✅ Correct color (green or yellow)
- ✅ Tooltip shows full details

### Test 2: Toast Notification Behavior
**Steps**:
1. Create session with unsupported runner
2. Observe toast notification

**Expected**:
- ✅ Toast appears at top center
- ✅ Shows requested and fallback runner
- ✅ Shows reason for fallback
- ✅ Can be dismissed manually
- ✅ Auto-dismisses after 10 seconds

### Test 3: Multiple Sessions
**Steps**:
1. Create session A with sandbox-exec
2. Create session B with exec
3. Create session C with firejail (fallback)
4. Switch between sessions

**Expected**:
- ✅ Each session shows correct badge
- ✅ Badge updates when switching
- ✅ Session A: 🔒 sandbox-exec
- ✅ Session B: ⚠️ exec
- ✅ Session C: ⚠️ exec (with toast on creation)

---

## Test Execution Checklist

- [ ] Scenario 1: exec Runner
- [ ] Scenario 2: sandbox-exec Runner
- [ ] Scenario 3: firejail Fallback
- [ ] Scenario 4: docker Runner (running)
- [ ] Scenario 5: docker Fallback (not running)
- [ ] Pre-flight Validation Tests
- [ ] UI/UX Tests

## Notes
- Record any unexpected behavior
- Check server logs for warnings
- Verify metadata in session storage
- Test on both web UI and CLI (if applicable)

