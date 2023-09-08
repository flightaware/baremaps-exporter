package main

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPrint(t *testing.T) {
	data, err := os.ReadFile("testdata/5-7-12.mvt")
	assert.Nil(t, err)
	printTile(data)
}
