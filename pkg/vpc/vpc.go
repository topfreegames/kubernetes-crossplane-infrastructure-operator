package vpc

import (
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/pkg/errors"
)

func GetVPCIdFromCIDR(region *string, CIDR string) (*string, error) {
	awsClient, err := session.NewSession(
		&aws.Config{
			Region: aws.String(*region),
		},
	)
	if err != nil {
		return nil, err
	}
	ec2svc := ec2.New(awsClient)
	filter := "cidr"
	result, err := ec2svc.DescribeVpcs(&ec2.DescribeVpcsInput{
		Filters: []*ec2.Filter{
			{
				Name: &filter,
				Values: []*string{
					&CIDR,
				},
			},
		},
	})
	if err != nil {
		return nil, err
	}

	if len(result.Vpcs) == 0 {
		return nil, errors.Wrap(err, "failed to retrieve vpc with CIDR")
	}

	return result.Vpcs[0].VpcId, nil
}
