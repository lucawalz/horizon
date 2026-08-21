package manager

import (
	"fmt"
	"os"
	"testing"

	"github.com/lucawalz/horizon/internal/testenv"
)

const (
	namespaceVar  = "POD_NAMESPACE"
	testNamespace = "horizon-system"
)

var testEnv *testenv.Environment

func TestMain(m *testing.M) {
	env, err := testenv.Start(Scheme())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	testEnv = env

	code := m.Run()
	stopWiredManager()

	if err := env.Stop(); err != nil {
		fmt.Fprintf(os.Stderr, "stop envtest: %v\n", err)
	}
	os.Exit(code)
}
