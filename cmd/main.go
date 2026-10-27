/*
Copyright 2023.

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
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	crossec2v1alpha1 "github.com/crossplane-contrib/provider-aws/apis/ec2/v1alpha1"
	crossec2v1beta1 "github.com/crossplane-contrib/provider-aws/apis/ec2/v1beta1"
	kcontrolplanev1alpha1 "github.com/topfreegames/kubernetes-kops-operator/apis/controlplane/v1alpha1"
	kinfrastructurev1alpha1 "github.com/topfreegames/kubernetes-kops-operator/apis/infrastructure/v1alpha1"
	"go.uber.org/zap/zapcore"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/utils/ptr"
	clusterv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"

	clustermeshv1alpha1 "github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/api/clustermesh.infrastructure/v1alpha1"
	securitygroupv1alpha2 "github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/api/ec2.aws/v1alpha2"
	clustermeshcontrollers "github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/internal/controller/clustermesh.infrastructure"
	sgcontroller "github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/internal/controller/ec2.aws"
	"github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/pkg/clusterfilter"
	"github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/pkg/dryrun"
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
	utilruntime.Must(securitygroupv1alpha2.AddToScheme(scheme))
	//+kubebuilder:scaffold:scheme
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	var dryRun bool
	var cluster string
	var once bool
	var timeout time.Duration
	var clusterMeshWorkerCount int
	var securityGroupWorkerCount int
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.BoolVar(&dryRun, "dry-run", false,
		"Reconcile without making changes. Kubernetes writes are sent with dryRun=All, "+
			"AWS and Spot writes and events are only logged. Implies --leader-elect=false and --metrics-bind-address=0.")
	flag.StringVar(&cluster, "cluster", "",
		"Reconcile only the objects of the cluster with this KopsControlPlane name.")
	flag.BoolVar(&once, "once", false,
		"With --dry-run, reconcile every object once, print the plan of the changes to stdout and exit. "+
			"Exits with 0 when there are no changes, 2 when there are changes and 1 when a reconcile fails.")
	flag.DurationVar(&timeout, "timeout", 15*time.Minute, "With --once, the longest time the plan can take.")
	flag.IntVar(&clusterMeshWorkerCount, "clustermesh-worker-count", 1, "The number of ClusterMesh reconciles that run at the same time.")
	flag.IntVar(&securityGroupWorkerCount, "securitygroup-worker-count", 5, "The number of SecurityGroup reconciles that run at the same time.")

	opts := zap.Options{
		Development: true,
		TimeEncoder: zapcore.TimeEncoderOfLayout(time.RFC3339),
	}
	opts.BindFlags(flag.CommandLine)
	// Exit with 1 on a wrong flag, not with the 2 of flag.ExitOnError, which
	// --once uses for a plan with changes.
	flag.CommandLine.Init(os.Args[0], flag.ContinueOnError)
	if err := flag.CommandLine.Parse(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		os.Exit(1)
	}

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	if once && !dryRun {
		setupLog.Error(nil, "--once requires --dry-run")
		os.Exit(1)
	}

	if timeout <= 0 {
		setupLog.Error(nil, "--timeout must be greater than 0")
		os.Exit(1)
	}

	if clusterMeshWorkerCount < 1 || securityGroupWorkerCount < 1 {
		setupLog.Error(nil, "--clustermesh-worker-count and --securitygroup-worker-count must be at least 1")
		os.Exit(1)
	}

	if dryRun {
		setupLog.Info("dry-run mode: disabling leader election and the metrics server")
	}

	if once {
		// The manager listens on the probe address when it is created, even
		// though --once does not start it.
		probeAddr = "0"
	}

	cfg := ctrl.GetConfigOrDie()
	cfg.QPS = 20
	cfg.Burst = 30
	mgr, err := ctrl.NewManager(cfg, managerOptions(metricsAddr, probeAddr, enableLeaderElection, dryRun))
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	clusterMeshReconciler := clustermeshcontrollers.DefaultReconciler(mgr)
	sgReconciler := sgcontroller.DefaultReconciler(mgr)
	if cluster != "" {
		setupLog.Info("reconciling only the objects of one cluster", "cluster", cluster)
		configureClusterFilter(clusterMeshReconciler, sgReconciler, cluster)
	}
	if dryRun {
		setupLog.Info("dry-run mode: no changes will be made to Kubernetes, AWS or Spot")
		configureDryRun(clusterMeshReconciler, sgReconciler, mgr.GetAPIReader())
	}

	if once {
		os.Exit(runOnce(mgr, clusterMeshReconciler, sgReconciler, clusterMeshWorkerCount, securityGroupWorkerCount, timeout, cluster))
	}

	if err = clusterMeshReconciler.SetupWithManager(mgr, clusterMeshWorkerCount); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ClusterMesh")
		os.Exit(1)
	}
	if err = sgReconciler.SetupWithManager(mgr, securityGroupWorkerCount); err != nil {
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

func managerOptions(metricsAddr, probeAddr string, enableLeaderElection, dryRun bool) ctrl.Options {
	if dryRun {
		enableLeaderElection = false
		metricsAddr = "0"
	}
	return ctrl.Options{
		Scheme: scheme,
		Metrics: server.Options{
			BindAddress: metricsAddr,
		},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "1334ff7b.infrastructure.wildlife.io",
		Controller: config.Controller{
			UsePriorityQueue: ptr.To(false),
		},
	}
}

// runOnce starts only the cache of the manager, plans every object once,
// prints the plan and returns the exit code.
func runOnce(mgr ctrl.Manager, clusterMeshReconciler *clustermeshcontrollers.ClusterMeshReconciler, sgReconciler *sgcontroller.SecurityGroupReconciler, clusterMeshWorkers, sgWorkers int, timeout time.Duration, clusterName string) int {
	ctx, cancel := context.WithTimeout(ctrl.SetupSignalHandler(), timeout)
	defer cancel()
	go func() {
		if err := mgr.GetCache().Start(ctx); err != nil {
			setupLog.Error(err, "problem running the cache")
		}
	}()

	plan := dryrun.NewPlan()
	err := fmt.Errorf("the cache did not start")
	if mgr.GetCache().WaitForCacheSync(ctx) {
		setupLog.Info("planning")
		err = plan.Once(ctx, mgr.GetClient(), clusterMeshReconciler, sgReconciler, clusterMeshWorkers, sgWorkers, clusterName)
	}
	if err != nil {
		// Show the error in the plan too, so that it does not look complete.
		plan.SetResult(dryrun.Source{Kind: "Plan", Name: "run"}, dryrun.StatusError, err.Error())
	}
	summary, writeErr := plan.Write(os.Stdout)
	if err != nil || writeErr != nil {
		setupLog.Error(errors.Join(err, writeErr), "plan failed")
		return dryrun.ExitError
	}
	return summary.ExitCode()
}

// configureClusterFilter makes the reconcilers skip the objects of other
// clusters than clusterName.
func configureClusterFilter(clusterMeshReconciler *clustermeshcontrollers.ClusterMeshReconciler, sgReconciler *sgcontroller.SecurityGroupReconciler, clusterName string) {
	clusterMeshReconciler.RequestFilter = clusterfilter.ClusterRequestFilter(clusterMeshReconciler.Client, clusterName)
	sgReconciler.RequestFilter = clusterfilter.SecurityGroupRequestFilter(sgReconciler.Client, clusterName)
}

func configureDryRun(clusterMeshReconciler *clustermeshcontrollers.ClusterMeshReconciler, sgReconciler *sgcontroller.SecurityGroupReconciler, apiReader client.Reader) {
	clusterMeshReconciler.Client = dryrun.NewClient(clusterMeshReconciler.Client, apiReader)
	clusterMeshReconciler.NewEC2ClientFactory = dryrun.EC2ClientFactory(clusterMeshReconciler.NewEC2ClientFactory)

	sgReconciler.Client = dryrun.NewClient(sgReconciler.Client, apiReader)
	sgReconciler.Recorder = dryrun.NewEventRecorder(ctrl.Log.WithName("securityGroup-controller"))
	sgReconciler.NewEC2ClientFactory = dryrun.EC2ClientFactory(sgReconciler.NewEC2ClientFactory)
	sgReconciler.NewAutoScalingClientFactory = dryrun.AutoScalingClientFactory(sgReconciler.NewAutoScalingClientFactory)
	sgReconciler.NewOceanCloudProviderAWSFactory = dryrun.OceanClientFactory(sgReconciler.NewOceanCloudProviderAWSFactory)
}
