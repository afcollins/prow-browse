package main

import (
	"testing"
)

func TestClassifyRun(t *testing.T) {
	tests := []struct {
		name string
		job  string
		want string
	}{
		{"aws job", "periodic-ci-openshift-eng-ocp-qe-perfscale-aws-4.22-e2e", "AWS"},
		{"rosa job", "periodic-ci-openshift-eng-ocp-qe-perfscale-rosa-classic-4.17", "ROSA"},
		{"rosa_hcp job", "periodic-ci-openshift-eng-ocp-qe-perfscale-rosa_hcp-4.17", "ROSA HCP"},
		{"hypershift job", "periodic-ci-openshift-eng-ocp-qe-perfscale-hypershift-test", "ROSA HCP"},
		{"vsphere job", "periodic-ci-openshift-eng-ocp-qe-perfscale-vsphere-4.17", "vSphere"},
		{"netobserv job", "periodic-ci-openshift-eng-ocp-qe-perfscale-netobserv-perf-tests-netobserv", "NetObserv"},
		{"metal-rhoso job", "periodic-ci-openshift-eng-ocp-qe-perfscale-metal-rhoso-test", "Metal RHOSO"},
		{"baremetal-multi job", "periodic-ci-openshift-eng-ocp-qe-perfscale-baremetal-multi-3nodes", "Baremetal Multi"},

		// Job type groups
		{"loaded-upgrade aws", "periodic-ci-openshift-eng-ocp-qe-perfscale-loaded-upgrade-aws-4.17", "!Loaded Upgrade / AWS"},
		{"loaded-upgrade rosa", "periodic-ci-openshift-eng-ocp-qe-perfscale-loaded-upgrade-rosa-4.17", "!Loaded Upgrade / ROSA"},
		{"loaded-upgrade rosa_hcp", "periodic-ci-openshift-eng-ocp-qe-perfscale-loaded-upgrade-rosa_hcp-4.17", "!Loaded Upgrade / ROSA HCP"},

		// Metal sub-groups
		{"metal daily-virt", "periodic-ci-openshift-eng-ocp-qe-perfscale-metal-daily-virt-4.17", "!Metal / daily-virt"},
		{"metal weekly", "periodic-ci-openshift-eng-ocp-qe-perfscale-metal-weekly-4.17", "!Metal / weekly"},
		{"metal weekly-telco-core-cpt", "periodic-ci-openshift-eng-ocp-qe-perfscale-metal-weekly-telco-core-cpt", "!Metal / weekly-telco-core-cpt"},
		{"metal weekly-eip", "periodic-ci-openshift-eng-ocp-qe-perfscale-metal-weekly-eip-4.17", "!Metal / weekly-eip"},
		{"metal udn-bgp", "periodic-ci-openshift-eng-ocp-qe-perfscale-metal-udn-bgp-4.17", "!Metal / udn-bgp"},

		// Unknown
		{"unknown job", "periodic-ci-openshift-eng-ocp-qe-perfscale-unknown-thing", "other"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyRun(RunResult{Job: tt.job})
			if got != tt.want {
				t.Errorf("classifyRun(%q) = %q, want %q", tt.job, got, tt.want)
			}
		})
	}
}

func TestDetectPlatform(t *testing.T) {
	tests := []struct {
		job  string
		want string
	}{
		{"some-aws-job", "AWS"},
		{"some-rosa_hcp-job", "ROSA HCP"},
		{"some-rosa-job", "ROSA"},
		{"some-vsphere-job", "vSphere"},
		{"some-hypershift-job", "ROSA HCP"},
		{"some-unknown-job", "other"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := detectPlatform(tt.job)
			if got != tt.want {
				t.Errorf("detectPlatform(%q) = %q, want %q", tt.job, got, tt.want)
			}
		})
	}
}

func TestDetectSubGroup(t *testing.T) {
	metalJT := jobTypeGroups[1] // Metal

	tests := []struct {
		name string
		job  string
		want string
	}{
		{"daily-virt", "metal-daily-virt-test", "daily-virt"},
		{"weekly", "metal-weekly-test", "weekly"},
		{"weekly-telco-core-cpt", "metal-weekly-telco-core-cpt-test", "weekly-telco-core-cpt"},
		{"weekly-eip", "metal-weekly-eip-test", "weekly-eip"},
		{"udn-bgp", "metal-udn-bgp-test", "udn-bgp"},
		{"falls back to platform", "metal-aws-test", "AWS"},
		{"unknown platform", "metal-unknown-test", "other"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := detectSubGroup(tt.job, metalJT)
			if got != tt.want {
				t.Errorf("detectSubGroup(%q) = %q, want %q", tt.job, got, tt.want)
			}
		})
	}
}

func TestIsStepForPlatform(t *testing.T) {
	tests := []struct {
		name  string
		step  string
		group string
		want  bool
	}{
		// showAllSteps groups (! prefix) always return true
		{"showAllSteps group", "anything", "!Metal / daily-virt", true},
		{"showAllSteps loaded-upgrade", "ipi-conf-aws", "!Loaded Upgrade / AWS", true},

		// AWS platform steps
		{"aws step on AWS page", "ipi-conf-aws-firewall", "AWS", true},
		{"ipi step on AWS page", "ipi-install-install", "AWS", true},
		{"aws step on ROSA page", "ipi-conf-aws-firewall", "ROSA", false},

		// vSphere platform steps
		{"vsphere step on vSphere page", "ipi-conf-vsphere-check", "vSphere", true},
		{"upi step on vSphere page", "upi-install-something", "vSphere", true},
		{"vsphere step on AWS page", "ipi-conf-vsphere-check", "AWS", false},

		// ROSA platform steps
		{"rosa step on ROSA page", "rosa-sts-setup", "ROSA", true},
		{"osd-ccs step on ROSA page", "osd-ccs-provision", "ROSA", true},
		{"rosa step on ROSA HCP page", "rosa-sts-setup", "ROSA HCP", true},
		{"rosa step on AWS page", "rosa-sts-setup", "AWS", false},

		// Common steps (no platform keyword) shown on all
		{"common step on AWS", "openshift-e2e-test", "AWS", true},
		{"common step on ROSA", "openshift-e2e-test", "ROSA", true},
		{"common step on vSphere", "openshift-e2e-test", "vSphere", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isStepForPlatform(tt.step, tt.group)
			if got != tt.want {
				t.Errorf("isStepForPlatform(%q, %q) = %v, want %v", tt.step, tt.group, got, tt.want)
			}
		})
	}
}

func TestDisplayGroupName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"!Metal / daily-virt", "Metal / daily-virt"},
		{"!Loaded Upgrade / AWS", "Loaded Upgrade / AWS"},
		{"AWS", "AWS"},
		{"ROSA", "ROSA"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := displayGroupName(tt.input)
			if got != tt.want {
				t.Errorf("displayGroupName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestOrderSteps(t *testing.T) {
	t.Run("config order first then alphabetical", func(t *testing.T) {
		allSteps := map[string]StepResult{
			"zebra":       StepSuccess,
			"alpha":       StepSuccess,
			"ipi-conf":    StepSuccess,
			"ipi-install": StepSuccess,
			"middle":      StepSuccess,
		}
		configOrder := []string{"ipi-conf", "ipi-install", "not-present"}

		got := orderSteps(allSteps, configOrder)
		want := []string{"ipi-conf", "ipi-install", "alpha", "middle", "zebra"}

		if len(got) != len(want) {
			t.Fatalf("orderSteps returned %d steps, want %d", len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("orderSteps[%d] = %q, want %q", i, got[i], want[i])
			}
		}
	})

	t.Run("empty config order", func(t *testing.T) {
		allSteps := map[string]StepResult{"c": StepSuccess, "a": StepSuccess, "b": StepSuccess}
		got := orderSteps(allSteps, nil)
		want := []string{"a", "b", "c"}

		for i := range want {
			if got[i] != want[i] {
				t.Errorf("orderSteps[%d] = %q, want %q", i, got[i], want[i])
			}
		}
	})

	t.Run("empty steps", func(t *testing.T) {
		got := orderSteps(make(map[string]StepResult), []string{"a", "b"})
		if len(got) != 0 {
			t.Errorf("orderSteps(empty) returned %d steps, want 0", len(got))
		}
	})
}

func TestIsStepExpectedForJob(t *testing.T) {
	makeResults := func(n int, stepPresent int) []RunResult {
		results := make([]RunResult, n)
		for i := range results {
			results[i].Pulled = true
			results[i].Steps = make(map[string]StepResult)
			if i < stepPresent {
				results[i].Steps["test-step"] = StepSuccess
			}
		}
		return results
	}

	tests := []struct {
		name    string
		total   int
		present int
		want    bool
	}{
		{"100% presence", 10, 10, true},
		{"50% presence", 10, 5, true},
		{"30% presence (boundary)", 10, 3, true},
		{"20% presence", 10, 2, false},
		{"0% presence", 10, 0, false},
		{"single result with step", 1, 1, true},
		{"single result without step", 1, 0, false},
		{"3 results 1 present (33%)", 3, 1, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results := makeResults(tt.total, tt.present)
			got := isStepExpectedForJob("test-step", results)
			if got != tt.want {
				t.Errorf("isStepExpectedForJob with %d/%d = %v, want %v", tt.present, tt.total, got, tt.want)
			}
		})
	}

	t.Run("unpulled runs excluded from threshold", func(t *testing.T) {
		results := []RunResult{
			{Pulled: true, Steps: map[string]StepResult{"test-step": StepSuccess}},
			{Pulled: true, Steps: map[string]StepResult{"test-step": StepSuccess}},
			{Pulled: false, Steps: map[string]StepResult{}}, // unpulled — should not count
			{Pulled: false, Steps: map[string]StepResult{}},
		}
		// 2/2 pulled have the step = 100%, should be expected
		got := isStepExpectedForJob("test-step", results)
		if !got {
			t.Error("expected step to be expected when only counting pulled runs")
		}
	})

	t.Run("all unpulled returns false", func(t *testing.T) {
		results := []RunResult{
			{Pulled: false, Steps: map[string]StepResult{}},
			{Pulled: false, Steps: map[string]StepResult{}},
		}
		got := isStepExpectedForJob("test-step", results)
		if got {
			t.Error("expected false when no runs are pulled")
		}
	})
}

func TestCountUniqueJobs(t *testing.T) {
	tests := []struct {
		name    string
		results []RunResult
		want    int
	}{
		{"empty", nil, 0},
		{"one job", []RunResult{{Job: "a"}, {Job: "a"}}, 1},
		{"two jobs", []RunResult{{Job: "a"}, {Job: "b"}}, 2},
		{"duplicates", []RunResult{{Job: "a"}, {Job: "b"}, {Job: "a"}, {Job: "c"}}, 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := countUniqueJobs(tt.results)
			if got != tt.want {
				t.Errorf("countUniqueJobs = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestShortJobName(t *testing.T) {
	cfg := &Config{JobPattern: "periodic-ci-openshift-eng-ocp-qe-perfscale"}

	tests := []struct {
		name string
		job  string
		want string
	}{
		{"with prefix", "periodic-ci-openshift-eng-ocp-qe-perfscale-aws-4.22", "-j aws-4.22"},
		{"without prefix", "some-other-job", "some-other-job"},
		{"exact prefix no suffix", "periodic-ci-openshift-eng-ocp-qe-perfscale-", "-j "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shortJobName(tt.job, cfg)
			if got != tt.want {
				t.Errorf("shortJobName(%q) = %q, want %q", tt.job, got, tt.want)
			}
		})
	}
}

func TestGetEmojiPalette(t *testing.T) {
	t.Run("known palette", func(t *testing.T) {
		p := getEmojiPalette("fruits")
		if len(p) != 16 {
			t.Errorf("fruits palette has %d emojis, want 16", len(p))
		}
	})

	t.Run("default palette", func(t *testing.T) {
		p := getEmojiPalette("default")
		if len(p) != 64 {
			t.Errorf("default palette has %d emojis, want 64", len(p))
		}
	})

	t.Run("unknown falls back to default", func(t *testing.T) {
		p := getEmojiPalette("nonexistent")
		if len(p) != 64 {
			t.Errorf("unknown palette should fall back to default (64), got %d", len(p))
		}
	})
}
