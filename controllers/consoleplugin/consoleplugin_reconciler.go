package consoleplugin

import (
	"context"
	"reflect"

	"github.com/netobserv/network-observability-operator/pkg/discover"
	osv1alpha1 "github.com/openshift/api/console/v1alpha1"
	operatorsv1 "github.com/openshift/api/operator/v1"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	appsv1 "k8s.io/api/apps/v1"
	ascv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/log"

	flowslatest "github.com/netobserv/network-observability-operator/api/v1beta1"
	"github.com/netobserv/network-observability-operator/controllers/constants"
	"github.com/netobserv/network-observability-operator/controllers/reconcilers"
	"github.com/netobserv/network-observability-operator/pkg/helper"
)

// Type alias
type pluginSpec = flowslatest.FlowCollectorConsolePlugin

// CPReconciler reconciles the current console plugin state with the desired configuration
type CPReconciler struct {
	reconcilers.ClientHelper
	nobjMngr      *reconcilers.NamespacedObjectManager
	owned         ownedObjects
	image         string
	availableAPIs *discover.AvailableAPIs
}

type ownedObjects struct {
	deployment     *appsv1.Deployment
	service        *corev1.Service
	hpa            *ascv2.HorizontalPodAutoscaler
	serviceAccount *corev1.ServiceAccount
	configMap      *corev1.ConfigMap
	serviceMonitor *monitoringv1.ServiceMonitor
}

func NewReconciler(cl reconcilers.ClientHelper, ns, prevNS, imageName string, availableAPIs *discover.AvailableAPIs) CPReconciler {
	owned := ownedObjects{
		deployment:     &appsv1.Deployment{},
		service:        &corev1.Service{},
		hpa:            &ascv2.HorizontalPodAutoscaler{},
		serviceAccount: &corev1.ServiceAccount{},
		configMap:      &corev1.ConfigMap{},
		serviceMonitor: &monitoringv1.ServiceMonitor{},
	}
	nobjMngr := reconcilers.NewNamespacedObjectManager(cl, ns, prevNS)
	nobjMngr.AddManagedObject(constants.PluginName, owned.deployment)
	nobjMngr.AddManagedObject(constants.PluginName, owned.service)
	nobjMngr.AddManagedObject(constants.PluginName, owned.hpa)
	nobjMngr.AddManagedObject(constants.PluginName, owned.serviceAccount)
	nobjMngr.AddManagedObject(configMapName, owned.configMap)
	if availableAPIs.HasSvcMonitor() {
		nobjMngr.AddManagedObject(constants.PluginName, owned.serviceMonitor)
	}

	return CPReconciler{ClientHelper: cl, nobjMngr: nobjMngr, owned: owned, image: imageName, availableAPIs: availableAPIs}
}

// CleanupNamespace cleans up old namespace
func (r *CPReconciler) CleanupNamespace(ctx context.Context) {
	r.nobjMngr.CleanupPreviousNamespace(ctx)
}

// Reconcile is the reconciler entry point to reconcile the current plugin state with the desired configuration
func (r *CPReconciler) Reconcile(ctx context.Context, desired *flowslatest.FlowCollector) error {
	ns := r.nobjMngr.Namespace
	// Retrieve current owned objects
	err := r.nobjMngr.FetchAll(ctx)
	if err != nil {
		return err
	}

	if err = r.checkAutoPatch(ctx, desired); err != nil {
		return err
	}

	// Create object builder
	builder := newBuilder(ns, r.image, &desired.Spec, r.CertWatcher)

	if err := r.reconcilePermissions(ctx, &builder); err != nil {
		return err
	}

	if err = r.reconcilePlugin(ctx, builder, &desired.Spec); err != nil {
		return err
	}

	cmDigest, err := r.reconcileConfigMap(ctx, builder, &desired.Spec)
	if err != nil {
		return err
	}

	if err = r.reconcileDeployment(ctx, builder, &desired.Spec, cmDigest); err != nil {
		return err
	}

	if err = r.reconcileService(ctx, builder, &desired.Spec); err != nil {
		return err
	}

	if err = r.reconcileHPA(ctx, builder, &desired.Spec); err != nil {
		return err
	}

	return nil
}

func (r *CPReconciler) checkAutoPatch(ctx context.Context, desired *flowslatest.FlowCollector) error {
	console := operatorsv1.Console{}
	if err := r.Client.Get(ctx, types.NamespacedName{Name: "cluster"}, &console); err != nil {
		// Console operator CR not found => warn but continue execution
		if desired.Spec.ConsolePlugin.Register {
			log.FromContext(ctx).Error(err, "Could not get the Console Operator resource for plugin registration. Please register manually.")
		}
		return nil
	}
	registered := helper.ContainsString(console.Spec.Plugins, constants.PluginName)
	if desired.Spec.ConsolePlugin.Register && !registered {
		console.Spec.Plugins = append(console.Spec.Plugins, constants.PluginName)
		return r.Client.Update(ctx, &console)
	} else if !desired.Spec.ConsolePlugin.Register && registered {
		console.Spec.Plugins = helper.RemoveAllStrings(console.Spec.Plugins, constants.PluginName)
		return r.Client.Update(ctx, &console)
	}
	return nil
}

func (r *CPReconciler) reconcilePermissions(ctx context.Context, builder *builder) error {
	if !r.nobjMngr.Exists(r.owned.serviceAccount) {
		return r.CreateOwned(ctx, builder.serviceAccount())
	} // update not needed for now

	cr := buildClusterRole()
	if err := r.ReconcileClusterRole(ctx, cr); err != nil {
		return err
	}

	desired := builder.clusterRoleBinding()
	if err := r.ReconcileClusterRoleBinding(ctx, desired); err != nil {
		return err
	}
	return nil
}

func (r *CPReconciler) reconcilePlugin(ctx context.Context, builder builder, desired *flowslatest.FlowCollectorSpec) error {
	// Console plugin is cluster-scope (it's not deployed in our namespace) however it must still be updated if our namespace changes
	oldPlg := osv1alpha1.ConsolePlugin{}
	pluginExists := true
	err := r.Get(ctx, types.NamespacedName{Name: constants.PluginName}, &oldPlg)
	if err != nil {
		if errors.IsNotFound(err) {
			pluginExists = false
		} else {
			return err
		}
	}

	// Check if objects need update
	consolePlugin := builder.consolePlugin()
	if !pluginExists {
		if err := r.CreateOwned(ctx, consolePlugin); err != nil {
			return err
		}
	} else if pluginNeedsUpdate(&oldPlg, &desired.ConsolePlugin) {
		if err := r.UpdateOwned(ctx, &oldPlg, consolePlugin); err != nil {
			return err
		}
	}
	return nil
}

func (r *CPReconciler) reconcileConfigMap(ctx context.Context, builder builder, desired *flowslatest.FlowCollectorSpec) (string, error) {
	newCM, configDigest := builder.configMap()
	if !r.nobjMngr.Exists(r.owned.configMap) {
		if err := r.CreateOwned(ctx, newCM); err != nil {
			return "", err
		}
	} else if !reflect.DeepEqual(newCM.Data, r.owned.configMap.Data) {
		if err := r.UpdateOwned(ctx, r.owned.configMap, newCM); err != nil {
			return "", err
		}
	}
	return configDigest, nil
}

func (r *CPReconciler) reconcileDeployment(ctx context.Context, builder builder, desired *flowslatest.FlowCollectorSpec, cmDigest string) error {
	report := helper.NewChangeReport("Console deployment")
	defer report.LogIfNeeded(ctx)

	newDepl := builder.deployment(cmDigest)
	// Annotate pod with certificate reference so that it is reloaded if modified
	if err := r.CertWatcher.AnnotatePod(ctx, r.Client, &newDepl.Spec.Template, lokiCerts, lokiStatusCerts); err != nil {
		return err
	}
	if !r.nobjMngr.Exists(r.owned.deployment) {
		if err := r.CreateOwned(ctx, newDepl); err != nil {
			return err
		}
	} else if helper.DeploymentChanged(r.owned.deployment, newDepl, constants.PluginName, helper.HPADisabled(&desired.ConsolePlugin.Autoscaler), desired.ConsolePlugin.Replicas, &report) {
		if err := r.UpdateOwned(ctx, r.owned.deployment, newDepl); err != nil {
			return err
		}
	} else {
		r.CheckDeploymentInProgress(r.owned.deployment)
	}
	return nil
}

func (r *CPReconciler) reconcileService(ctx context.Context, builder builder, desired *flowslatest.FlowCollectorSpec) error {
	report := helper.NewChangeReport("Console service")
	defer report.LogIfNeeded(ctx)

	if !r.nobjMngr.Exists(r.owned.service) {
		newSVC := builder.service(nil)
		if err := r.CreateOwned(ctx, newSVC); err != nil {
			return err
		}
	} else if serviceNeedsUpdate(r.owned.service, &desired.ConsolePlugin, &report) {
		newSVC := builder.service(r.owned.service)
		if err := r.UpdateOwned(ctx, r.owned.service, newSVC); err != nil {
			return err
		}
	}
	if r.availableAPIs.HasSvcMonitor() {
		serviceMonitor := builder.serviceMonitor()
		if err := reconcilers.GenericReconcile(ctx, r.nobjMngr, &r.ClientHelper, r.owned.serviceMonitor, serviceMonitor, &report, helper.ServiceMonitorChanged); err != nil {
			return err
		}
	}
	return nil
}

func (r *CPReconciler) reconcileHPA(ctx context.Context, builder builder, desired *flowslatest.FlowCollectorSpec) error {
	report := helper.NewChangeReport("Console autoscaler")
	defer report.LogIfNeeded(ctx)

	// Delete or Create / Update Autoscaler according to HPA option
	if helper.HPADisabled(&desired.ConsolePlugin.Autoscaler) {
		r.nobjMngr.TryDelete(ctx, r.owned.hpa)
	} else {
		newASC := builder.autoScaler()
		if !r.nobjMngr.Exists(r.owned.hpa) {
			if err := r.CreateOwned(ctx, newASC); err != nil {
				return err
			}
		} else if helper.AutoScalerChanged(r.owned.hpa, desired.ConsolePlugin.Autoscaler, &report) {
			if err := r.UpdateOwned(ctx, r.owned.hpa, newASC); err != nil {
				return err
			}
		}
	}
	return nil
}

func pluginNeedsUpdate(plg *osv1alpha1.ConsolePlugin, desired *pluginSpec) bool {
	return plg.Spec.Service.Port != desired.Port
}

func querierURL(loki *flowslatest.FlowCollectorLoki) string {
	if loki.QuerierURL != "" {
		return loki.QuerierURL
	}
	return loki.URL
}

func statusURL(loki *flowslatest.FlowCollectorLoki) string {
	if loki.StatusURL != "" {
		return loki.StatusURL
	}
	return querierURL(loki)
}

func serviceNeedsUpdate(svc *corev1.Service, desired *pluginSpec, report *helper.ChangeReport) bool {
	for _, port := range svc.Spec.Ports {
		if port.Port == desired.Port && port.Protocol == "TCP" {
			return false
		}
	}
	report.Add("Port changed")
	return true
}
