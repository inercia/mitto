// Command list-conversations is a minimal example of the Mitto Go client
// (github.com/inercia/mitto/pkg/api): it lists every conversation known to a
// Mitto server and prints one line per conversation.
//
//	go run ./examples/go-client/list-conversations -url http://localhost:8080
//
// If the server requires authentication, pass -token or set MITTO_TOKEN.
package main

import (
	"flag"
	"fmt"
	"os"

	client "github.com/inercia/mitto/pkg/api"
)

func main() {
	url := flag.String("url", "http://localhost:8080", "Mitto server base URL")
	token := flag.String("token", os.Getenv("MITTO_TOKEN"), "shared bearer token (default: $MITTO_TOKEN)")
	flag.Parse()

	var opts []client.Option
	if *token != "" {
		opts = append(opts, client.WithBearerToken(*token))
	}
	c := client.New(*url, opts...)

	sessions, err := c.ListSessions()
	if err != nil {
		fmt.Fprintf(os.Stderr, "list conversations: %v\n", err)
		os.Exit(1)
	}

	if len(sessions) == 0 {
		fmt.Println("no conversations")
		return
	}
	for _, s := range sessions {
		fmt.Printf("%s\t%s\t%s\n", s.SessionID, s.Status, s.Name)
	}
}
