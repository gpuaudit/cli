// Copyright 2026 the gpuaudit authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package aws

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/gpuaudit/cli/internal/models"
)

// SpotPriceClient is the subset of the EC2 API needed for spot price lookups.
type SpotPriceClient interface {
	DescribeSpotPriceHistory(ctx context.Context, params *ec2.DescribeSpotPriceHistoryInput, optFns ...func(*ec2.Options)) (*ec2.DescribeSpotPriceHistoryOutput, error)
}

// EnrichSpotPrices fetches current spot prices for EC2 GPU instances and
// populates SpotHourlyCost on each instance where spot is available.
func EnrichSpotPrices(ctx context.Context, client SpotPriceClient, instances []models.GPUInstance) {
	// Collect unique EC2 instance types.
	typeSet := make(map[string]bool)
	for _, inst := range instances {
		if inst.Source == models.SourceEC2 {
			typeSet[inst.InstanceType] = true
		}
	}
	if len(typeSet) == 0 {
		return
	}

	instanceTypes := make([]ec2types.InstanceType, 0, len(typeSet))
	for t := range typeSet {
		instanceTypes = append(instanceTypes, ec2types.InstanceType(t))
	}

	input := &ec2.DescribeSpotPriceHistoryInput{
		InstanceTypes:       instanceTypes,
		ProductDescriptions: []string{"Linux/UNIX"},
		StartTime:           aws.Time(time.Now().Add(-1 * time.Hour)),
	}

	out, err := client.DescribeSpotPriceHistory(ctx, input)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  warning: could not fetch spot prices: %v\n", err)
		return
	}

	// Take the most recent price per instance type (API returns newest first).
	latestPrice := make(map[string]float64)
	for _, sp := range out.SpotPriceHistory {
		itype := string(sp.InstanceType)
		if _, seen := latestPrice[itype]; seen {
			continue
		}
		price, err := strconv.ParseFloat(aws.ToString(sp.SpotPrice), 64)
		if err != nil {
			continue
		}
		latestPrice[itype] = price
	}

	// Populate SpotHourlyCost on matching instances.
	for i := range instances {
		if instances[i].Source != models.SourceEC2 {
			continue
		}
		if price, ok := latestPrice[instances[i].InstanceType]; ok {
			instances[i].SpotHourlyCost = &price
		}
	}
}
