#!/usr/bin/env python3
"""
Simple test to verify resume capability in mock ACP server.
Tests the JSON-RPC protocol directly.
"""

import json
import subprocess
import sys
import time

def send_request(proc, method, params=None, id_num=1):
    """Send a JSON-RPC request and return the response."""
    request = {
        "jsonrpc": "2.0",
        "id": id_num,
        "method": method
    }
    if params is not None:
        request["params"] = params
    
    request_str = json.dumps(request) + "\n"
    print(f"→ Sending: {method}", file=sys.stderr)
    
    proc.stdin.write(request_str.encode())
    proc.stdin.flush()
    
    # Read response
    response_str = proc.stdout.readline().decode()
    if not response_str:
        return None
    
    response = json.loads(response_str)
    print(f"← Received: {response.get('result', {}).get('serverInfo', {}).get('name', 'response')}", file=sys.stderr)
    return response

def main():
    # Start mock server
    proc = subprocess.Popen(
        ["./tests/mocks/acp-server/mock-acp-server", "--verbose"],
        stdin=subprocess.PIPE,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        bufsize=0
    )
    
    try:
        # Test 1: Initialize and check for resume capability
        print("\n=== Test 1: Initialize ===")
        init_response = send_request(proc, "initialize", {
            "protocolVersion": 1,
            "clientInfo": {"name": "test-client", "version": "1.0.0"}
        }, 1)
        
        if not init_response:
            print("✗ No response from initialize")
            return 1
        
        # Check for resume capability
        agent_caps = init_response.get("result", {}).get("agentCapabilities", {})
        session_caps = agent_caps.get("sessionCapabilities", {})
        has_resume = "resume" in session_caps and session_caps["resume"] is not None
        
        if has_resume:
            print("✓ Resume capability advertised")
            print(f"  sessionCapabilities: {json.dumps(session_caps, indent=2)}")
        else:
            print("✗ Resume capability NOT found")
            print(f"  agentCapabilities: {json.dumps(agent_caps, indent=2)}")
            return 1
        
        # Test 2: Create a new session
        print("\n=== Test 2: Create session ===")
        new_session_response = send_request(proc, "session/new", {
            "cwd": "/tmp/test"
        }, 2)
        
        if not new_session_response:
            print("✗ No response from session/new")
            return 1
        
        result = new_session_response.get("result", {})
        session_id = result.get("sessionId")
        modes = result.get("modes")
        
        if not session_id:
            print("✗ No session ID in response")
            print(f"  Response: {json.dumps(new_session_response, indent=2)}")
            return 1
        
        print(f"✓ Session created: {session_id}")
        if modes:
            print(f"  Current mode: {modes.get('currentModeId')}")
        
        # Test 3: Resume the session
        print("\n=== Test 3: Resume session ===")
        resume_response = send_request(proc, "session/unstableResumeSession", {
            "sessionId": session_id,
            "cwd": "/tmp/test"
        }, 3)
        
        if not resume_response:
            print("✗ No response from session/unstableResumeSession")
            return 1
        
        # Check for error
        if "error" in resume_response:
            error = resume_response["error"]
            print(f"✗ Resume failed with error: {error.get('message')}")
            print(f"  Error: {json.dumps(error, indent=2)}")
            return 1
        
        result = resume_response.get("result", {})
        resumed_modes = result.get("modes")
        config_options = result.get("configOptions")
        
        print("✓ Session resumed successfully")
        if resumed_modes:
            print(f"  Resumed mode: {resumed_modes.get('currentModeId')}")
        if config_options is not None:
            print(f"  Config options: {len(config_options)} options")
        
        # Test 4: Try to resume non-existent session
        print("\n=== Test 4: Resume non-existent session ===")
        bad_resume_response = send_request(proc, "session/unstableResumeSession", {
            "sessionId": "non-existent-session-12345",
            "cwd": "/tmp/test"
        }, 4)
        
        if not bad_resume_response:
            print("✗ No response from bad resume attempt")
            return 1
        
        if "error" in bad_resume_response:
            error = bad_resume_response["error"]
            error_msg = error.get("message", "")
            if "not found" in error_msg.lower() or "garbage collected" in error_msg.lower():
                print(f"✓ Correctly rejected non-existent session")
                print(f"  Error message: {error_msg}")
            else:
                print(f"✗ Unexpected error message: {error_msg}")
                return 1
        else:
            print("✗ Expected error for non-existent session, got success")
            return 1
        
        # Shutdown
        print("\n=== Shutting down ===")
        send_request(proc, "shutdown", None, 5)
        
        print("\n" + "="*50)
        print("✓ ALL TESTS PASSED")
        print("="*50)
        return 0
        
    finally:
        proc.terminate()
        proc.wait(timeout=2)
        
        # Print stderr (server logs)
        stderr = proc.stderr.read().decode()
        if stderr:
            print("\n=== Server logs ===", file=sys.stderr)
            print(stderr, file=sys.stderr)

if __name__ == "__main__":
    sys.exit(main())
