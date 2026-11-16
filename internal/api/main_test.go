package api

import (
	"net"
	"os"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/bigjakk/nexara/internal/auth"
)

// TestMain hashes at bcrypt's cheapest cost: under the race detector a single
// hash at the production cost takes seconds. A test that needs a hash to take
// real time sets its own cost with auth.SetBcryptCostForTesting.
func TestMain(m *testing.M) {
	auth.SetBcryptCostForTesting(bcrypt.MinCost)
	// Installed before any test starts, so arming it later races nothing; see sweepOutbound.
	net.DefaultResolver = sweepOutbound.resolver()
	os.Exit(m.Run())
}
