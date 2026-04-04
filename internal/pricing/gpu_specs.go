// Package pricing provides GPU instance type specifications and pricing data.
package pricing

// GPUSpec describes the GPU hardware in a specific instance type.
type GPUSpec struct {
	InstanceType  string  // e.g. "p5.48xlarge"
	GPUModel      string  // e.g. "H100"
	GPUCount      int
	GPUVRAMGiB    float64 // per GPU
	TotalVRAMGiB  float64 // total across all GPUs
	VCPUs         int
	MemoryGiB     float64
	OnDemandHourly float64 // USD, us-east-1 baseline
	Family        string  // e.g. "p5", "g6", "g5"
}

// awsGPUSpecs contains known AWS GPU instance types and their specifications.
// Prices are on-demand us-east-1 as of Q1 2026.
var awsGPUSpecs = map[string]GPUSpec{
	// H100 instances (p5)
	"p5.48xlarge": {InstanceType: "p5.48xlarge", GPUModel: "H100 SXM", GPUCount: 8, GPUVRAMGiB: 80, TotalVRAMGiB: 640, VCPUs: 192, MemoryGiB: 2048, OnDemandHourly: 98.32, Family: "p5"},

	// A100 instances (p4d/p4de)
	"p4d.24xlarge":  {InstanceType: "p4d.24xlarge", GPUModel: "A100 40GB", GPUCount: 8, GPUVRAMGiB: 40, TotalVRAMGiB: 320, VCPUs: 96, MemoryGiB: 1152, OnDemandHourly: 32.77, Family: "p4d"},
	"p4de.24xlarge": {InstanceType: "p4de.24xlarge", GPUModel: "A100 80GB", GPUCount: 8, GPUVRAMGiB: 80, TotalVRAMGiB: 640, VCPUs: 96, MemoryGiB: 1152, OnDemandHourly: 40.97, Family: "p4de"},

	// V100 instances (p3) - still common
	"p3.2xlarge":  {InstanceType: "p3.2xlarge", GPUModel: "V100", GPUCount: 1, GPUVRAMGiB: 16, TotalVRAMGiB: 16, VCPUs: 8, MemoryGiB: 61, OnDemandHourly: 3.06, Family: "p3"},
	"p3.8xlarge":  {InstanceType: "p3.8xlarge", GPUModel: "V100", GPUCount: 4, GPUVRAMGiB: 16, TotalVRAMGiB: 64, VCPUs: 32, MemoryGiB: 244, OnDemandHourly: 12.24, Family: "p3"},
	"p3.16xlarge": {InstanceType: "p3.16xlarge", GPUModel: "V100", GPUCount: 8, GPUVRAMGiB: 16, TotalVRAMGiB: 128, VCPUs: 64, MemoryGiB: 488, OnDemandHourly: 24.48, Family: "p3"},

	// L40S instances (g6e)
	"g6e.xlarge":   {InstanceType: "g6e.xlarge", GPUModel: "L40S", GPUCount: 1, GPUVRAMGiB: 48, TotalVRAMGiB: 48, VCPUs: 4, MemoryGiB: 32, OnDemandHourly: 1.86, Family: "g6e"},
	"g6e.2xlarge":  {InstanceType: "g6e.2xlarge", GPUModel: "L40S", GPUCount: 1, GPUVRAMGiB: 48, TotalVRAMGiB: 48, VCPUs: 8, MemoryGiB: 64, OnDemandHourly: 2.35, Family: "g6e"},
	"g6e.4xlarge":  {InstanceType: "g6e.4xlarge", GPUModel: "L40S", GPUCount: 1, GPUVRAMGiB: 48, TotalVRAMGiB: 48, VCPUs: 16, MemoryGiB: 128, OnDemandHourly: 3.34, Family: "g6e"},
	"g6e.8xlarge":  {InstanceType: "g6e.8xlarge", GPUModel: "L40S", GPUCount: 1, GPUVRAMGiB: 48, TotalVRAMGiB: 48, VCPUs: 32, MemoryGiB: 256, OnDemandHourly: 5.31, Family: "g6e"},
	"g6e.12xlarge": {InstanceType: "g6e.12xlarge", GPUModel: "L40S", GPUCount: 4, GPUVRAMGiB: 48, TotalVRAMGiB: 192, VCPUs: 48, MemoryGiB: 384, OnDemandHourly: 13.80, Family: "g6e"},
	"g6e.16xlarge": {InstanceType: "g6e.16xlarge", GPUModel: "L40S", GPUCount: 1, GPUVRAMGiB: 48, TotalVRAMGiB: 48, VCPUs: 64, MemoryGiB: 512, OnDemandHourly: 9.25, Family: "g6e"},
	"g6e.24xlarge": {InstanceType: "g6e.24xlarge", GPUModel: "L40S", GPUCount: 4, GPUVRAMGiB: 48, TotalVRAMGiB: 192, VCPUs: 96, MemoryGiB: 768, OnDemandHourly: 18.36, Family: "g6e"},
	"g6e.48xlarge": {InstanceType: "g6e.48xlarge", GPUModel: "L40S", GPUCount: 8, GPUVRAMGiB: 48, TotalVRAMGiB: 384, VCPUs: 192, MemoryGiB: 1536, OnDemandHourly: 36.72, Family: "g6e"},

	// L4 instances (g6)
	"g6.xlarge":   {InstanceType: "g6.xlarge", GPUModel: "L4", GPUCount: 1, GPUVRAMGiB: 24, TotalVRAMGiB: 24, VCPUs: 4, MemoryGiB: 16, OnDemandHourly: 0.8048, Family: "g6"},
	"g6.2xlarge":  {InstanceType: "g6.2xlarge", GPUModel: "L4", GPUCount: 1, GPUVRAMGiB: 24, TotalVRAMGiB: 24, VCPUs: 8, MemoryGiB: 32, OnDemandHourly: 0.9776, Family: "g6"},
	"g6.4xlarge":  {InstanceType: "g6.4xlarge", GPUModel: "L4", GPUCount: 1, GPUVRAMGiB: 24, TotalVRAMGiB: 24, VCPUs: 16, MemoryGiB: 64, OnDemandHourly: 1.3232, Family: "g6"},
	"g6.8xlarge":  {InstanceType: "g6.8xlarge", GPUModel: "L4", GPUCount: 1, GPUVRAMGiB: 24, TotalVRAMGiB: 24, VCPUs: 32, MemoryGiB: 128, OnDemandHourly: 2.0144, Family: "g6"},
	"g6.12xlarge": {InstanceType: "g6.12xlarge", GPUModel: "L4", GPUCount: 4, GPUVRAMGiB: 24, TotalVRAMGiB: 96, VCPUs: 48, MemoryGiB: 192, OnDemandHourly: 4.6016, Family: "g6"},
	"g6.16xlarge": {InstanceType: "g6.16xlarge", GPUModel: "L4", GPUCount: 1, GPUVRAMGiB: 24, TotalVRAMGiB: 24, VCPUs: 64, MemoryGiB: 256, OnDemandHourly: 3.3968, Family: "g6"},
	"g6.24xlarge": {InstanceType: "g6.24xlarge", GPUModel: "L4", GPUCount: 4, GPUVRAMGiB: 24, TotalVRAMGiB: 96, VCPUs: 96, MemoryGiB: 384, OnDemandHourly: 7.1456, Family: "g6"},
	"g6.48xlarge": {InstanceType: "g6.48xlarge", GPUModel: "L4", GPUCount: 8, GPUVRAMGiB: 24, TotalVRAMGiB: 192, VCPUs: 192, MemoryGiB: 768, OnDemandHourly: 13.35, Family: "g6"},

	// A10G instances (g5)
	"g5.xlarge":   {InstanceType: "g5.xlarge", GPUModel: "A10G", GPUCount: 1, GPUVRAMGiB: 24, TotalVRAMGiB: 24, VCPUs: 4, MemoryGiB: 16, OnDemandHourly: 1.006, Family: "g5"},
	"g5.2xlarge":  {InstanceType: "g5.2xlarge", GPUModel: "A10G", GPUCount: 1, GPUVRAMGiB: 24, TotalVRAMGiB: 24, VCPUs: 8, MemoryGiB: 32, OnDemandHourly: 1.212, Family: "g5"},
	"g5.4xlarge":  {InstanceType: "g5.4xlarge", GPUModel: "A10G", GPUCount: 1, GPUVRAMGiB: 24, TotalVRAMGiB: 24, VCPUs: 16, MemoryGiB: 64, OnDemandHourly: 1.624, Family: "g5"},
	"g5.8xlarge":  {InstanceType: "g5.8xlarge", GPUModel: "A10G", GPUCount: 1, GPUVRAMGiB: 24, TotalVRAMGiB: 24, VCPUs: 32, MemoryGiB: 128, OnDemandHourly: 2.448, Family: "g5"},
	"g5.12xlarge": {InstanceType: "g5.12xlarge", GPUModel: "A10G", GPUCount: 4, GPUVRAMGiB: 24, TotalVRAMGiB: 96, VCPUs: 48, MemoryGiB: 192, OnDemandHourly: 5.672, Family: "g5"},
	"g5.16xlarge": {InstanceType: "g5.16xlarge", GPUModel: "A10G", GPUCount: 1, GPUVRAMGiB: 24, TotalVRAMGiB: 24, VCPUs: 64, MemoryGiB: 256, OnDemandHourly: 4.096, Family: "g5"},
	"g5.24xlarge": {InstanceType: "g5.24xlarge", GPUModel: "A10G", GPUCount: 4, GPUVRAMGiB: 24, TotalVRAMGiB: 96, VCPUs: 96, MemoryGiB: 384, OnDemandHourly: 8.144, Family: "g5"},
	"g5.48xlarge": {InstanceType: "g5.48xlarge", GPUModel: "A10G", GPUCount: 8, GPUVRAMGiB: 24, TotalVRAMGiB: 192, VCPUs: 192, MemoryGiB: 768, OnDemandHourly: 16.288, Family: "g5"},

	// T4 instances (g4dn)
	"g4dn.xlarge":   {InstanceType: "g4dn.xlarge", GPUModel: "T4", GPUCount: 1, GPUVRAMGiB: 16, TotalVRAMGiB: 16, VCPUs: 4, MemoryGiB: 16, OnDemandHourly: 0.526, Family: "g4dn"},
	"g4dn.2xlarge":  {InstanceType: "g4dn.2xlarge", GPUModel: "T4", GPUCount: 1, GPUVRAMGiB: 16, TotalVRAMGiB: 16, VCPUs: 8, MemoryGiB: 32, OnDemandHourly: 0.752, Family: "g4dn"},
	"g4dn.4xlarge":  {InstanceType: "g4dn.4xlarge", GPUModel: "T4", GPUCount: 1, GPUVRAMGiB: 16, TotalVRAMGiB: 16, VCPUs: 16, MemoryGiB: 64, OnDemandHourly: 1.204, Family: "g4dn"},
	"g4dn.8xlarge":  {InstanceType: "g4dn.8xlarge", GPUModel: "T4", GPUCount: 1, GPUVRAMGiB: 16, TotalVRAMGiB: 16, VCPUs: 32, MemoryGiB: 128, OnDemandHourly: 2.176, Family: "g4dn"},
	"g4dn.12xlarge": {InstanceType: "g4dn.12xlarge", GPUModel: "T4", GPUCount: 4, GPUVRAMGiB: 16, TotalVRAMGiB: 64, VCPUs: 48, MemoryGiB: 192, OnDemandHourly: 3.912, Family: "g4dn"},
	"g4dn.16xlarge": {InstanceType: "g4dn.16xlarge", GPUModel: "T4", GPUCount: 1, GPUVRAMGiB: 16, TotalVRAMGiB: 16, VCPUs: 64, MemoryGiB: 256, OnDemandHourly: 4.352, Family: "g4dn"},
	"g4dn.metal":    {InstanceType: "g4dn.metal", GPUModel: "T4", GPUCount: 8, GPUVRAMGiB: 16, TotalVRAMGiB: 128, VCPUs: 96, MemoryGiB: 384, OnDemandHourly: 7.824, Family: "g4dn"},

	// Inferentia instances (inf2)
	"inf2.xlarge":   {InstanceType: "inf2.xlarge", GPUModel: "Inferentia2", GPUCount: 1, GPUVRAMGiB: 32, TotalVRAMGiB: 32, VCPUs: 4, MemoryGiB: 16, OnDemandHourly: 0.7582, Family: "inf2"},
	"inf2.8xlarge":  {InstanceType: "inf2.8xlarge", GPUModel: "Inferentia2", GPUCount: 1, GPUVRAMGiB: 32, TotalVRAMGiB: 32, VCPUs: 32, MemoryGiB: 128, OnDemandHourly: 1.9678, Family: "inf2"},
	"inf2.24xlarge": {InstanceType: "inf2.24xlarge", GPUModel: "Inferentia2", GPUCount: 6, GPUVRAMGiB: 32, TotalVRAMGiB: 192, VCPUs: 96, MemoryGiB: 384, OnDemandHourly: 6.4908, Family: "inf2"},
	"inf2.48xlarge": {InstanceType: "inf2.48xlarge", GPUModel: "Inferentia2", GPUCount: 12, GPUVRAMGiB: 32, TotalVRAMGiB: 384, VCPUs: 192, MemoryGiB: 768, OnDemandHourly: 12.9816, Family: "inf2"},

	// Trainium instances (trn1)
	"trn1.2xlarge":  {InstanceType: "trn1.2xlarge", GPUModel: "Trainium", GPUCount: 1, GPUVRAMGiB: 32, TotalVRAMGiB: 32, VCPUs: 8, MemoryGiB: 32, OnDemandHourly: 1.3438, Family: "trn1"},
	"trn1.32xlarge": {InstanceType: "trn1.32xlarge", GPUModel: "Trainium", GPUCount: 16, GPUVRAMGiB: 32, TotalVRAMGiB: 512, VCPUs: 128, MemoryGiB: 512, OnDemandHourly: 21.50, Family: "trn1"},
}

// SageMaker instance types map to the same GPU hardware but with "ml." prefix.
var sagemakerGPUSpecs map[string]GPUSpec

func init() {
	sagemakerGPUSpecs = make(map[string]GPUSpec, len(awsGPUSpecs))
	for k, v := range awsGPUSpecs {
		smKey := "ml." + k
		spec := v
		spec.InstanceType = smKey
		sagemakerGPUSpecs[smKey] = spec
	}
}

// LookupEC2 returns the GPU spec for an EC2 instance type, or nil if not a known GPU type.
func LookupEC2(instanceType string) *GPUSpec {
	if spec, ok := awsGPUSpecs[instanceType]; ok {
		return &spec
	}
	return nil
}

// LookupSageMaker returns the GPU spec for a SageMaker instance type, or nil if unknown.
func LookupSageMaker(instanceType string) *GPUSpec {
	if spec, ok := sagemakerGPUSpecs[instanceType]; ok {
		return &spec
	}
	return nil
}

// AllEC2Specs returns all known EC2 GPU instance specs.
func AllEC2Specs() []GPUSpec {
	specs := make([]GPUSpec, 0, len(awsGPUSpecs))
	for _, s := range awsGPUSpecs {
		specs = append(specs, s)
	}
	return specs
}

// GPUFamilies returns the set of EC2 instance family prefixes that contain GPUs.
func GPUFamilies() []string {
	return []string{"p3", "p4d", "p4de", "p5", "g4dn", "g5", "g6", "g6e", "inf2", "trn1"}
}

// SmallerAlternatives returns cheaper single-GPU instance types that could
// handle the workload, ordered by relevance. Same-family alternatives come
// first (e.g. g6e.xlarge for a g6e.12xlarge), then same-GPU-model from other
// families, then other GPUs. Within each tier, sorted by price ascending.
func SmallerAlternatives(current GPUSpec) []GPUSpec {
	var sameFamily, sameGPU, other []GPUSpec
	for _, spec := range awsGPUSpecs {
		if spec.GPUCount != 1 || spec.OnDemandHourly >= current.OnDemandHourly {
			continue
		}
		switch {
		case spec.Family == current.Family:
			sameFamily = append(sameFamily, spec)
		case spec.GPUModel == current.GPUModel:
			sameGPU = append(sameGPU, spec)
		default:
			other = append(other, spec)
		}
	}

	sortByPrice := func(s []GPUSpec) {
		for i := 0; i < len(s); i++ {
			for j := i + 1; j < len(s); j++ {
				if s[j].OnDemandHourly < s[i].OnDemandHourly {
					s[i], s[j] = s[j], s[i]
				}
			}
		}
	}
	sortByPrice(sameFamily)
	sortByPrice(sameGPU)
	sortByPrice(other)

	alts := make([]GPUSpec, 0, len(sameFamily)+len(sameGPU)+len(other))
	alts = append(alts, sameFamily...)
	alts = append(alts, sameGPU...)
	alts = append(alts, other...)
	return alts
}
