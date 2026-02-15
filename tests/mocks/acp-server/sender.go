package main

import (
	"encoding/json"
	"fmt"
	"sync"
)

var writeMu sync.Mutex

func (s *MockACPServer) send(msg interface{}) error {
	writeMu.Lock()
	defer writeMu.Unlock()

	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal error: %w", err)
	}

	s.log("Sending: %s", string(data))
	_, err = fmt.Fprintf(s.writer, "%s\n", data)
	return err
}

func (s *MockACPServer) sendResponse(id interface{}, result interface{}) error {
	return s.send(JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	})
}

func (s *MockACPServer) sendError(id interface{}, code int, message string, data interface{}) error {
	return s.send(JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &RPCError{
			Code:    code,
			Message: message,
			Data:    data,
		},
	})
}

func (s *MockACPServer) sendSessionUpdate(update SessionUpdate) error {
	notification := SessionNotification{
		JSONRPC: "2.0",
		Method:  "session/update", // SDK uses "session/update" method name
	}
	notification.Params.SessionID = s.sessionID
	notification.Params.Update = update

	return s.send(notification)
}

func (s *MockACPServer) sendNotification(notification SessionNotification) error {
	return s.send(notification)
}
