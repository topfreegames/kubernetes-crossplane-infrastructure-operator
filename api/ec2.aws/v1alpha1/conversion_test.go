package v1alpha1

import (
	"testing"

	securitygroupv1alpha2 "github.com/topfreegames/provider-crossplane/api/ec2.aws/v1alpha2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestConvertTo(t *testing.T) {

	testCases := []struct {
		description    string
		input          *SecurityGroup
		expectedOutput *securitygroupv1alpha2.SecurityGroup
		expectedError  error
	}{
		{
			description: "should convert a SecurityGroup v1alpha1 to a SecurityGroup v1alpha2",
			input: &SecurityGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-sg",
				},
				Spec: SecurityGroupSpec{
					IngressRules: []IngressRule{
						{
							IPProtocol: "tcp",
							FromPort:   0,
							ToPort:     0,
							AllowedCIDRBlocks: []string{
								"0.0.0.0/0",
							},
						},
					},
					InfrastructureRef: &corev1.ObjectReference{
						Name:       "test-infrastructure",
						Namespace:  corev1.NamespaceDefault,
						APIVersion: "dummy",
						Kind:       "MachinePool",
					},
				},
				Status: SecurityGroupStatus{
					Ready: true,
				},
			},
			expectedOutput: &securitygroupv1alpha2.SecurityGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-sg",
				},
				Spec: securitygroupv1alpha2.SecurityGroupSpec{
					IngressRules: []securitygroupv1alpha2.IngressRule{
						{
							IPProtocol: "tcp",
							FromPort:   0,
							ToPort:     0,
							AllowedCIDRBlocks: []string{
								"0.0.0.0/0",
							},
						},
					},
					InfrastructureRef: []*corev1.ObjectReference{
						{
							Name:       "test-infrastructure",
							Namespace:  corev1.NamespaceDefault,
							APIVersion: "dummy",
							Kind:       "MachinePool",
						},
					},
				},
				Status: securitygroupv1alpha2.SecurityGroupStatus{
					Ready: true,
				},
			},
		},
	}

	RegisterFailHandler(Fail)
	g := NewWithT(t)

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			hub := &securitygroupv1alpha2.SecurityGroup{}
			err := tc.input.ConvertTo(hub)
			if tc.expectedError != nil {
				g.Expect(err).To(MatchError(tc.expectedError))
			} else {
				g.Expect(err).To(BeNil())
				g.Expect(hub).To(BeEquivalentTo(tc.expectedOutput))
			}

		})
	}

}

func TestConvertFrom(t *testing.T) {

	testCases := []struct {
		description    string
		input          *securitygroupv1alpha2.SecurityGroup
		expectedOutput *SecurityGroup
		expectedError  error
	}{
		{
			description: "should convert a SecurityGroup v1alpha2 to a SecurityGroup v1alpha1",
			input: &securitygroupv1alpha2.SecurityGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-sg",
				},
				Spec: securitygroupv1alpha2.SecurityGroupSpec{
					IngressRules: []securitygroupv1alpha2.IngressRule{
						{
							IPProtocol: "tcp",
							FromPort:   0,
							ToPort:     0,
							AllowedCIDRBlocks: []string{
								"0.0.0.0/0",
							},
						},
					},
					InfrastructureRef: []*corev1.ObjectReference{
						{
							Name:       "test-infrastructure",
							Namespace:  corev1.NamespaceDefault,
							APIVersion: "dummy",
							Kind:       "MachinePool",
						},
					},
				},
				Status: securitygroupv1alpha2.SecurityGroupStatus{
					Ready: true,
				},
			},
			expectedOutput: &SecurityGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-sg",
				},
				Spec: SecurityGroupSpec{
					IngressRules: []IngressRule{
						{
							IPProtocol: "tcp",
							FromPort:   0,
							ToPort:     0,
							AllowedCIDRBlocks: []string{
								"0.0.0.0/0",
							},
						},
					},
					InfrastructureRef: &corev1.ObjectReference{
						Name:       "test-infrastructure",
						Namespace:  corev1.NamespaceDefault,
						APIVersion: "dummy",
						Kind:       "MachinePool",
					},
				},
				Status: SecurityGroupStatus{
					Ready: true,
				},
			},
		},
	}

	RegisterFailHandler(Fail)
	g := NewWithT(t)

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			spoke := &SecurityGroup{}
			err := spoke.ConvertFrom(tc.input)
			if tc.expectedError != nil {
				g.Expect(err).To(MatchError(tc.expectedError))
			} else {
				g.Expect(err).To(BeNil())
				g.Expect(spoke).To(BeEquivalentTo(tc.expectedOutput))
			}

		})
	}

}
