// Copyright 2026 the gpuaudit authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package pricing

import "testing"

func TestLookupEC2_KnownType(t *testing.T) {
	spec := LookupEC2("g5.xlarge")
	if spec == nil {
		t.Fatal("expected spec for g5.xlarge")
	}
	if spec.GPUModel != "A10G" {
		t.Errorf("expected A10G, got %s", spec.GPUModel)
	}
	if spec.GPUCount != 1 {
		t.Errorf("expected 1 GPU, got %d", spec.GPUCount)
	}
	if spec.OnDemandHourly <= 0 {
		t.Error("expected positive price")
	}
}

func TestLookupEC2_UnknownType(t *testing.T) {
	spec := LookupEC2("m5.xlarge")
	if spec != nil {
		t.Error("expected nil for non-GPU instance type")
	}
}

func TestLookupSageMaker_KnownType(t *testing.T) {
	spec := LookupSageMaker("ml.g5.xlarge")
	if spec == nil {
		t.Fatal("expected spec for ml.g5.xlarge")
	}
	if spec.GPUModel != "A10G" {
		t.Errorf("expected A10G, got %s", spec.GPUModel)
	}
}

func TestLookupSageMaker_UnknownType(t *testing.T) {
	spec := LookupSageMaker("ml.m5.xlarge")
	if spec != nil {
		t.Error("expected nil for non-GPU SageMaker type")
	}
}

func TestSmallerAlternatives(t *testing.T) {
	spec := LookupEC2("p5.48xlarge")
	if spec == nil {
		t.Fatal("expected spec for p5.48xlarge")
	}

	alts := SmallerAlternatives(*spec)
	if len(alts) == 0 {
		t.Fatal("expected alternatives for p5.48xlarge")
	}

	// All alternatives should be single-GPU and cheaper
	for _, alt := range alts {
		if alt.GPUCount != 1 {
			t.Errorf("expected single-GPU alternative, got %d GPUs for %s", alt.GPUCount, alt.InstanceType)
		}
		if alt.OnDemandHourly >= spec.OnDemandHourly {
			t.Errorf("alternative %s ($%.2f) is not cheaper than %s ($%.2f)",
				alt.InstanceType, alt.OnDemandHourly, spec.InstanceType, spec.OnDemandHourly)
		}
	}
}

func TestSmallerAlternatives_PrefersSameFamily(t *testing.T) {
	// g6e.12xlarge has 4× L40S — alternatives should start with g6e single-GPU instances
	spec := LookupEC2("g6e.12xlarge")
	if spec == nil {
		t.Fatal("expected spec for g6e.12xlarge")
	}

	alts := SmallerAlternatives(*spec)
	if len(alts) == 0 {
		t.Fatal("expected alternatives")
	}

	// First alternative should be same family (g6e)
	if alts[0].Family != "g6e" {
		t.Errorf("expected first alternative to be g6e family, got %s (%s)", alts[0].Family, alts[0].InstanceType)
	}
}

func TestGPUFamilies(t *testing.T) {
	families := GPUFamilies()
	if len(families) == 0 {
		t.Fatal("expected GPU families")
	}

	expected := map[string]bool{"g5": true, "g6": true, "p5": true, "g4dn": true}
	for k := range expected {
		found := false
		for _, f := range families {
			if f == k {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected family %s in list", k)
		}
	}
}

func TestAllEC2Specs_NotEmpty(t *testing.T) {
	specs := AllEC2Specs()
	if len(specs) < 30 {
		t.Errorf("expected at least 30 GPU specs, got %d", len(specs))
	}
}
