/*
//+build linux
*/
package integration

import (
	"github.com/stretchr/testify/assert"
)

func (s *WorkerSuite) TestSomething() {
	assert.True(s.T(), true, "True = true!")
}
