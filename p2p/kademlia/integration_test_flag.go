package kademlia

import "os"

func integrationTestEnabled() bool {
	return os.Getenv("INTEGRATION_TEST") == "true"
}
