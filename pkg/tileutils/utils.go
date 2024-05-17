package tileutils

import (
	"encoding/json"
	"fmt"

	"github.com/paulmach/orb/encoding/mvt"
)

func printTile(data []byte) {
	layers, err := mvt.Unmarshal(data)
	if err != nil {
		fmt.Printf("[error] %v", err)
		return
	}
	for _, l := range layers {
		jsonBytes, err := json.MarshalIndent(l, "", "    ")
		if err != nil {
			fmt.Printf("[error] layer=%s, %v", l.Name, err)
			continue
		}
		fmt.Println((string(jsonBytes)))
	}
}

func floatToString(input []float64) []string {
	out := make([]string, len(input))
	for i := range input {
		out[i] = fmt.Sprintf("%f", input[i])
	}
	return out
}
