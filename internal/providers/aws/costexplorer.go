package aws

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
	cetypes "github.com/aws/aws-sdk-go-v2/service/costexplorer/types"

	"github.com/maksimov/gpuaudit/internal/models"
)

// CostExplorerClient is the subset of the Cost Explorer API we need.
type CostExplorerClient interface {
	GetCostAndUsage(ctx context.Context, params *costexplorer.GetCostAndUsageInput, optFns ...func(*costexplorer.Options)) (*costexplorer.GetCostAndUsageOutput, error)
}

// EnrichCostData populates month-to-date actual cost from Cost Explorer,
// grouped by instance type. This gives us real spend numbers instead of
// relying solely on on-demand pricing estimates.
func EnrichCostData(ctx context.Context, client CostExplorerClient, instances []models.GPUInstance) error {
	if len(instances) == 0 {
		return nil
	}

	// Collect unique instance types
	typeSet := make(map[string]bool)
	for _, inst := range instances {
		if inst.InstanceType != "" {
			typeSet[inst.InstanceType] = true
		}
	}
	if len(typeSet) == 0 {
		return nil
	}

	var instanceTypes []string
	for t := range typeSet {
		instanceTypes = append(instanceTypes, t)
	}

	now := time.Now()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	end := now.Format("2006-01-02")
	start := monthStart.Format("2006-01-02")

	// If we're on the 1st of the month, look at last month instead
	if now.Day() == 1 {
		lastMonth := monthStart.AddDate(0, -1, 0)
		start = lastMonth.Format("2006-01-02")
		end = monthStart.Format("2006-01-02")
	}

	// Query EC2 cost grouped by instance type
	ec2Costs, err := getServiceCosts(ctx, client, start, end, "Amazon Elastic Compute Cloud - Compute", instanceTypes)
	if err != nil {
		return fmt.Errorf("querying EC2 costs: %w", err)
	}

	// Query SageMaker cost grouped by instance type
	smCosts, err := getServiceCosts(ctx, client, start, end, "Amazon SageMaker", instanceTypes)
	if err != nil {
		return fmt.Errorf("querying SageMaker costs: %w", err)
	}

	// Merge cost maps
	for k, v := range smCosts {
		ec2Costs[k] += v
	}

	// Apply costs to instances. Cost Explorer groups by instance type not
	// individual instance, so we split evenly across instances of the same type.
	typeCounts := make(map[string]int)
	for _, inst := range instances {
		typeCounts[inst.InstanceType]++
	}

	for i := range instances {
		inst := &instances[i]
		totalCost, ok := ec2Costs[inst.InstanceType]
		if !ok || totalCost == 0 {
			continue
		}
		perInstance := totalCost / float64(typeCounts[inst.InstanceType])
		inst.MTDCost = &perInstance
	}

	return nil
}

func getServiceCosts(ctx context.Context, client CostExplorerClient, start, end, service string, instanceTypes []string) (map[string]float64, error) {
	out, err := client.GetCostAndUsage(ctx, &costexplorer.GetCostAndUsageInput{
		TimePeriod: &cetypes.DateInterval{
			Start: aws.String(start),
			End:   aws.String(end),
		},
		Granularity: cetypes.GranularityMonthly,
		Metrics:     []string{"UnblendedCost"},
		GroupBy: []cetypes.GroupDefinition{
			{
				Type: cetypes.GroupDefinitionTypeDimension,
				Key:  aws.String("INSTANCE_TYPE"),
			},
		},
		Filter: &cetypes.Expression{
			And: []cetypes.Expression{
				{
					Dimensions: &cetypes.DimensionValues{
						Key:    cetypes.DimensionService,
						Values: []string{service},
					},
				},
				{
					Dimensions: &cetypes.DimensionValues{
						Key:    cetypes.DimensionInstanceType,
						Values: instanceTypes,
					},
				},
			},
		},
	})
	if err != nil {
		return nil, err
	}

	costs := make(map[string]float64)
	for _, result := range out.ResultsByTime {
		for _, group := range result.Groups {
			if len(group.Keys) == 0 {
				continue
			}
			instanceType := group.Keys[0]
			if amount, ok := group.Metrics["UnblendedCost"]; ok {
				val, _ := strconv.ParseFloat(aws.ToString(amount.Amount), 64)
				costs[instanceType] += val
			}
		}
	}
	return costs, nil
}
