package api

import (
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
	os.Exit(m.Run())
}
