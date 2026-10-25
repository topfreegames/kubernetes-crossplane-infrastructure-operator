package main

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	clustermeshcontrollers "github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/internal/controller/clustermesh.infrastructure"
	sgcontroller "github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/internal/controller/ec2.aws"
	"github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/pkg/aws/autoscaling"
	fakeasg "github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/pkg/aws/autoscaling/fake"
	"github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/pkg/aws/ec2"
	fakeec2 "github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/pkg/aws/ec2/fake"
	"github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/pkg/dryrun"
	"github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/pkg/spot"
	fakeocean "github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/pkg/spot/fake"
)

func TestConfigureDryRun(t *testing.T) {
	g := NewWithT(t)

	realClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	newEC2Client := func(cfg aws.Config) ec2.EC2Client { return &fakeec2.MockEC2Client{} }

	clusterMeshReconciler := &clustermeshcontrollers.ClusterMeshReconciler{
		Client:              realClient,
		NewEC2ClientFactory: newEC2Client,
	}
	sgReconciler := &sgcontroller.SecurityGroupReconciler{
		Client:                          realClient,
		Recorder:                        record.NewFakeRecorder(10),
		NewEC2ClientFactory:             newEC2Client,
		NewAutoScalingClientFactory:     func(cfg aws.Config) autoscaling.AutoScalingClient { return &fakeasg.MockAutoScalingClient{} },
		NewOceanCloudProviderAWSFactory: func() spot.OceanClient { return &fakeocean.MockOceanCloudProviderAWS{} },
	}

	configureDryRun(clusterMeshReconciler, sgReconciler)

	ctx := context.TODO()
	for _, c := range []client.Client{clusterMeshReconciler.Client, sgReconciler.Client} {
		cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "cm", Namespace: "default"}}
		g.Expect(c.Create(ctx, cm)).To(Succeed())
		err := realClient.Get(ctx, client.ObjectKeyFromObject(cm), &corev1.ConfigMap{})
		g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
	}

	g.Expect(clusterMeshReconciler.NewEC2ClientFactory(aws.Config{})).To(BeAssignableToTypeOf(&dryrun.EC2Client{}))

	g.Expect(sgReconciler.Recorder).NotTo(BeAssignableToTypeOf(&record.FakeRecorder{}))
	g.Expect(sgReconciler.NewEC2ClientFactory(aws.Config{})).To(BeAssignableToTypeOf(&dryrun.EC2Client{}))
	g.Expect(sgReconciler.NewAutoScalingClientFactory(aws.Config{})).To(BeAssignableToTypeOf(&dryrun.AutoScalingClient{}))
	g.Expect(sgReconciler.NewOceanCloudProviderAWSFactory()).To(BeAssignableToTypeOf(&dryrun.OceanClient{}))
}
