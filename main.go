/*
Copyright 2022.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"flag"
	"os"

	kcontrolplanev1alpha1 "github.com/topfreegames/kubernetes-kops-operator/apis/controlplane/v1alpha1"
	kinfrastructurev1alpha1 "github.com/topfreegames/kubernetes-kops-operator/apis/infrastructure/v1alpha1"
	clustermeshv1alpha1 "github.com/topfreegames/provider-crossplane/apis/clustermesh/v1alpha1"
	securitygroupv1alpha1 "github.com/topfreegames/provider-crossplane/apis/securitygroup/v1alpha1"
	clustermeshcontrollers "github.com/topfreegames/provider-crossplane/controllers/clustermesh"
	sgcontroller "github.com/topfreegames/provider-crossplane/controllers/securitygroup"
	"github.com/topfreegames/provider-crossplane/pkg/aws/autoscaling"
	"github.com/topfreegames/provider-crossplane/pkg/aws/ec2"
	"github.com/topfreegames/provider-crossplane/pkg/crossplane"

	crossec2v1alpha1 "github.com/crossplane/provider-aws/apis/ec2/v1alpha1"
	crossec2v1beta1 "github.com/crossplane/provider-aws/apis/ec2/v1beta1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	clusterv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	//+kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(crossec2v1beta1.SchemeBuilder.AddToScheme(scheme))
	utilruntime.Must(crossec2v1alpha1.SchemeBuilder.AddToScheme(scheme))
	utilruntime.Must(kinfrastructurev1alpha1.AddToScheme(scheme))
	utilruntime.Must(kcontrolplanev1alpha1.AddToScheme(scheme))
	utilruntime.Must(clusterv1beta1.AddToScheme(scheme))
	utilruntime.Must(clustermeshv1alpha1.AddToScheme(scheme))
	utilruntime.Must(securitygroupv1alpha1.AddToScheme(scheme))
	//+kubebuilder:scaffold:scheme
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		MetricsBindAddress:     metricsAddr,
		Port:                   9443,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "1334ff7b.infrastructure.wildlife.io",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if err = (&clustermeshcontrollers.ClusterMeshReconciler{
		Client:                         mgr.GetClient(),
		Scheme:                         mgr.GetScheme(),
		NewEC2ClientFactory:            ec2.NewEC2Client,
		PopulateClusterSpecFactory:     clustermeshcontrollers.PopulateClusterSpec,
		ReconcilePeeringsFactory:       clustermeshcontrollers.ReconcilePeerings,
		ReconcileSecurityGroupsFactory: clustermeshcontrollers.ReconcileSecurityGroups,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ClusterMesh")
		os.Exit(1)
	}
	if err = (&sgcontroller.SecurityGroupReconciler{
		Client:                      mgr.GetClient(),
		Scheme:                      mgr.GetScheme(),
		Recorder:                    mgr.GetEventRecorderFor("securityGroup-controller"),
		NewEC2ClientFactory:         ec2.NewEC2Client,
		NewAutoScalingClientFactory: autoscaling.NewAutoScalingClient,
		ManageCrossplaneSGFactory:   crossplane.ManageCrossplaneSecurityGroupResource,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "SecurityGroup")
		os.Exit(1)
	}

	//+kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
