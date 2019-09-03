package gcontainerd

import (
	"bufio"
	"fmt"
	"os"
)

type idMap string

const defaultUIDMap idMap = "/proc/self/uid_map"
const defaultGIDMap idMap = "/proc/self/gid_map"

const maxInt = ^uint(0) >> 1

func (u idMap) MaxValid() (uint32, error) {
	f, err := os.Open(string(u))
	if err != nil {
		return 0, err
	}

	var m uint
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var container, host, size uint
		if _, err := fmt.Sscanf(scanner.Text(), "%d %d %d", &container, &host, &size); err != nil {
			return 0, err
		}

		m = minUint(maxUint(m, container+size-1), maxInt)
	}

	return uint32(m), nil
}

func maxUint(a, b uint) uint {
	if a > b {
		return a
	}

	return b
}

func minUint(a, b uint) uint {
	if a < b {
		return a
	}

	return b
}

