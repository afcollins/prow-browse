package main

import (
	"encoding/json"
	"os"
)

type State struct {
	SeenRuns map[string][]string `json:"seen_runs"` // job name -> list of seen run IDs
}

func loadState(path string) (*State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &State{SeenRuns: make(map[string][]string)}, nil
		}
		return nil, err
	}

	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	if state.SeenRuns == nil {
		state.SeenRuns = make(map[string][]string)
	}
	return &state, nil
}

func saveState(path string, state *State) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
