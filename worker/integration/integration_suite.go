package integration

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	w "github.com/concourse/concourse/cmd"
)

type WorkerSuite struct {
	suite.Suite
	*require.Assertions

	workerCommand *w.WorkerCommand
}

func (s *WorkerSuite) SetupSuite() {
	fmt.Println("setup suite")

}

func (s *WorkerSuite) TearDownSuite() {
	fmt.Println("teardown suite")
}

func (s *WorkerSuite) SetupTest() {
	fmt.Println("setup test")
}

func (s *WorkerSuite) TearDownTest() {
	fmt.Println("teardown suite")
}

func TestSuite(t *testing.T) {
	suite.Run(t, &WorkerSuite{
		Assertions: require.New(t),
	})
}
