package lib

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/figwood/litmus-go/pkg/cerrors"
	"github.com/palantir/stacktrace"

	clients "github.com/figwood/litmus-go/pkg/clients"
	"github.com/figwood/litmus-go/pkg/events"
	experimentTypes "github.com/figwood/litmus-go/pkg/generic/node-memory-hog/types"
	"github.com/figwood/litmus-go/pkg/log"
	"github.com/figwood/litmus-go/pkg/probe"
	"github.com/figwood/litmus-go/pkg/status"
	"github.com/figwood/litmus-go/pkg/types"
	"github.com/figwood/litmus-go/pkg/utils/common"
	"github.com/figwood/litmus-go/pkg/utils/stringutils"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	apiv1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PrepareNodeMemoryHog contains preparation steps before chaos injection
func PrepareNodeMemoryHog(experimentsDetails *experimentTypes.ExperimentDetails, clients clients.ClientSets, resultDetails *types.ResultDetails, eventsDetails *types.EventDetails, chaosDetails *types.ChaosDetails) error {

	//set up the tunables if provided in range
	setChaosTunables(experimentsDetails)

	log.InfoWithValues("[Info]: The details of chaos tunables are:", logrus.Fields{
		"MemoryConsumptionMebibytes":  experimentsDetails.MemoryConsumptionMebibytes,
		"MemoryConsumptionPercentage": experimentsDetails.MemoryConsumptionPercentage,
		"NumberOfWorkers":             experimentsDetails.NumberOfWorkers,
		"Node Affected Percentage":    experimentsDetails.NodesAffectedPerc,
		"Sequence":                    experimentsDetails.Sequence,
	})

	//Waiting for the ramp time before chaos injection
	if experimentsDetails.RampTime != 0 {
		log.Infof("[Ramp]: Waiting for the %vs ramp time before injecting chaos", experimentsDetails.RampTime)
		common.WaitForDuration(experimentsDetails.RampTime)
	}

	//Select node for node-memory-hog
	nodesAffectedPerc, _ := strconv.Atoi(experimentsDetails.NodesAffectedPerc)
	targetNodeList, err := common.GetNodeList(experimentsDetails.TargetNodes, experimentsDetails.NodeLabel, nodesAffectedPerc, clients)
	if err != nil {
		return stacktrace.Propagate(err, "could not get node list")
	}

	log.InfoWithValues("[Info]: Details of Nodes under chaos injection", logrus.Fields{
		"No. Of Nodes": len(targetNodeList),
		"Node Names":   targetNodeList,
	})

	if experimentsDetails.EngineName != "" {
		if err := common.SetHelperData(chaosDetails, experimentsDetails.SetHelperData, clients); err != nil {
			return stacktrace.Propagate(err, "could not set helper data")
		}
	}

	switch strings.ToLower(experimentsDetails.Sequence) {
	case "serial":
		if err = injectChaosInSerialMode(experimentsDetails, targetNodeList, clients, resultDetails, eventsDetails, chaosDetails); err != nil {
			return stacktrace.Propagate(err, "could not run chaos in serial mode")
		}
	case "parallel":
		if err = injectChaosInParallelMode(experimentsDetails, targetNodeList, clients, resultDetails, eventsDetails, chaosDetails); err != nil {
			return stacktrace.Propagate(err, "could not run chaos in parallel mode")
		}
	default:
		return cerrors.Error{ErrorCode: cerrors.ErrorTypeGeneric, Reason: fmt.Sprintf("'%s' sequence is not supported", experimentsDetails.Sequence)}
	}

	//Waiting for the ramp time after chaos injection
	if experimentsDetails.RampTime != 0 {
		log.Infof("[Ramp]: Waiting for the %vs ramp time after injecting chaos", experimentsDetails.RampTime)
		common.WaitForDuration(experimentsDetails.RampTime)
	}
	return nil
}

// injectChaosInSerialMode stress the memory of all the target nodes serially (one by one)
func injectChaosInSerialMode(experimentsDetails *experimentTypes.ExperimentDetails, targetNodeList []string, clients clients.ClientSets, resultDetails *types.ResultDetails, eventsDetails *types.EventDetails, chaosDetails *types.ChaosDetails) error {

	// run the probes during chaos
	if len(resultDetails.ProbeDetails) != 0 {
		if err := probe.RunProbes(chaosDetails, clients, resultDetails, "DuringChaos", eventsDetails); err != nil {
			return err
		}
	}

	for _, appNode := range targetNodeList {

		if experimentsDetails.EngineName != "" {
			msg := "Injecting " + experimentsDetails.ExperimentName + " chaos on " + appNode + " node"
			types.SetEngineEventAttributes(eventsDetails, types.ChaosInject, msg, "Normal", chaosDetails)
			events.GenerateEvents(eventsDetails, clients, chaosDetails, "ChaosEngine")
		}

		log.InfoWithValues("[Info]: Details of Node under chaos injection", logrus.Fields{
			"NodeName":                      appNode,
			"Memory Consumption Percentage": experimentsDetails.MemoryConsumptionPercentage,
			"Memory Consumption Mebibytes":  experimentsDetails.MemoryConsumptionMebibytes,
		})

		experimentsDetails.RunID = stringutils.GetRunID()

		//Getting node memory details
		memoryCapacity, memoryAllocatable, err := getNodeMemoryDetails(appNode, clients)
		if err != nil {
			return stacktrace.Propagate(err, "could not get node memory details")
		}

		//Getting the exact memory value to exhaust
		MemoryConsumption, err := calculateMemoryConsumption(experimentsDetails, memoryCapacity, memoryAllocatable)
		if err != nil {
			return stacktrace.Propagate(err, "could not calculate memory consumption value")
		}

		// Creating the helper pod to perform node memory hog
		if err = createHelperPod(experimentsDetails, chaosDetails, appNode, clients, MemoryConsumption); err != nil {
			return stacktrace.Propagate(err, "could not create helper pod")
		}

		appLabel := fmt.Sprintf("app=%s-helper-%s", experimentsDetails.ExperimentName, experimentsDetails.RunID)

		//Checking the status of helper pod
		log.Info("[Status]: Checking the status of the helper pod")
		if err := status.CheckHelperStatus(experimentsDetails.ChaosNamespace, appLabel, experimentsDetails.Timeout, experimentsDetails.Delay, clients); err != nil {
			common.DeleteAllHelperPodBasedOnJobCleanupPolicy(appLabel, chaosDetails, clients)
			return stacktrace.Propagate(err, "could not check helper status")
		}

		common.SetTargets(appNode, "targeted", "node", chaosDetails)

		// Wait till the completion of helper pod
		log.Info("[Wait]: Waiting till the completion of the helper pod")
		podStatus, err := status.WaitForCompletion(experimentsDetails.ChaosNamespace, appLabel, clients, experimentsDetails.ChaosDuration+experimentsDetails.Timeout, experimentsDetails.ExperimentName)
		if err != nil {
			common.DeleteAllHelperPodBasedOnJobCleanupPolicy(appLabel, chaosDetails, clients)
			return common.HelperFailedError(err, appLabel, chaosDetails.ChaosNamespace, false)
		} else if podStatus == "Failed" {
			common.DeleteAllHelperPodBasedOnJobCleanupPolicy(appLabel, chaosDetails, clients)
			return errors.Errorf("helper pod status is %v", podStatus)
		}

		//Deleting the helper pod
		log.Info("[Cleanup]: Deleting the helper pod")
		if err := common.DeleteAllPod(appLabel, experimentsDetails.ChaosNamespace, chaosDetails.Timeout, chaosDetails.Delay, clients); err != nil {
			return stacktrace.Propagate(err, "could not delete helper pod(s)")
		}
	}
	return nil
}

// injectChaosInParallelMode stress the memory all the target nodes in parallel mode (all at once)
func injectChaosInParallelMode(experimentsDetails *experimentTypes.ExperimentDetails, targetNodeList []string, clients clients.ClientSets, resultDetails *types.ResultDetails, eventsDetails *types.EventDetails, chaosDetails *types.ChaosDetails) error {

	// run the probes during chaos
	if len(resultDetails.ProbeDetails) != 0 {
		if err := probe.RunProbes(chaosDetails, clients, resultDetails, "DuringChaos", eventsDetails); err != nil {
			return err
		}
	}

	experimentsDetails.RunID = stringutils.GetRunID()

	for _, appNode := range targetNodeList {

		if experimentsDetails.EngineName != "" {
			msg := "Injecting " + experimentsDetails.ExperimentName + " chaos on " + appNode + " node"
			types.SetEngineEventAttributes(eventsDetails, types.ChaosInject, msg, "Normal", chaosDetails)
			events.GenerateEvents(eventsDetails, clients, chaosDetails, "ChaosEngine")
		}

		log.InfoWithValues("[Info]: Details of Node under chaos injection", logrus.Fields{
			"NodeName":                      appNode,
			"Memory Consumption Percentage": experimentsDetails.MemoryConsumptionPercentage,
			"Memory Consumption Mebibytes":  experimentsDetails.MemoryConsumptionMebibytes,
		})

		//Getting node memory details
		memoryCapacity, memoryAllocatable, err := getNodeMemoryDetails(appNode, clients)
		if err != nil {
			return stacktrace.Propagate(err, "could not get node memory details")
		}

		//Getting the exact memory value to exhaust
		MemoryConsumption, err := calculateMemoryConsumption(experimentsDetails, memoryCapacity, memoryAllocatable)
		if err != nil {
			return stacktrace.Propagate(err, "could not calculate memory consumption value")
		}

		// Creating the helper pod to perform node memory hog
		if err = createHelperPod(experimentsDetails, chaosDetails, appNode, clients, MemoryConsumption); err != nil {
			return stacktrace.Propagate(err, "could not create helper pod")
		}
	}

	appLabel := fmt.Sprintf("app=%s-helper-%s", experimentsDetails.ExperimentName, experimentsDetails.RunID)

	//Checking the status of helper pod
	log.Info("[Status]: Checking the status of the helper pod")
	if err := status.CheckHelperStatus(experimentsDetails.ChaosNamespace, appLabel, experimentsDetails.Timeout, experimentsDetails.Delay, clients); err != nil {
		common.DeleteAllHelperPodBasedOnJobCleanupPolicy(appLabel, chaosDetails, clients)
		return stacktrace.Propagate(err, "could not check helper status")
	}

	for _, appNode := range targetNodeList {
		common.SetTargets(appNode, "targeted", "node", chaosDetails)
	}

	// Wait till the completion of helper pod
	log.Info("[Wait]: Waiting till the completion of the helper pod")
	podStatus, err := status.WaitForCompletion(experimentsDetails.ChaosNamespace, appLabel, clients, experimentsDetails.ChaosDuration+experimentsDetails.Timeout, common.GetContainerNames(chaosDetails)...)
	if err != nil || podStatus == "Failed" {
		common.DeleteAllHelperPodBasedOnJobCleanupPolicy(appLabel, chaosDetails, clients)
		return common.HelperFailedError(err, appLabel, chaosDetails.ChaosNamespace, false)
	}

	//Deleting the helper pod
	log.Info("[Cleanup]: Deleting the helper pod")
	if err = common.DeleteAllPod(appLabel, experimentsDetails.ChaosNamespace, chaosDetails.Timeout, chaosDetails.Delay, clients); err != nil {
		return stacktrace.Propagate(err, "could not delete helper pod(s)")
	}

	return nil
}

// getNodeMemoryDetails will return the total memory capacity and memory allocatable of an application node
func getNodeMemoryDetails(appNodeName string, clients clients.ClientSets) (int, int, error) {

	nodeDetails, err := clients.KubeClient.CoreV1().Nodes().Get(context.Background(), appNodeName, v1.GetOptions{})
	if err != nil {
		return 0, 0, cerrors.Error{ErrorCode: cerrors.ErrorTypeGeneric, Target: fmt.Sprintf("{nodeName: %s}", appNodeName), Reason: err.Error()}
	}

	memoryCapacity := int(nodeDetails.Status.Capacity.Memory().Value())
	memoryAllocatable := int(nodeDetails.Status.Allocatable.Memory().Value())

	if memoryCapacity == 0 || memoryAllocatable == 0 {
		return memoryCapacity, memoryAllocatable, cerrors.Error{ErrorCode: cerrors.ErrorTypeGeneric, Target: fmt.Sprintf("{nodeName: %s}", appNodeName), Reason: "failed to get memory details of the target node"}
	}

	return memoryCapacity, memoryAllocatable, nil
}

// calculateMemoryConsumption will calculate the amount of memory to be consumed for a given unit.
func calculateMemoryConsumption(experimentsDetails *experimentTypes.ExperimentDetails, memoryCapacity, memoryAllocatable int) (string, error) {

	var totalMemoryConsumption int
	var MemoryConsumption string
	var selector string

	if experimentsDetails.MemoryConsumptionMebibytes == "0" {
		if experimentsDetails.MemoryConsumptionPercentage == "0" {
			log.Info("Neither of MemoryConsumptionPercentage or MemoryConsumptionMebibytes provided, proceeding with a default MemoryConsumptionPercentage value of 30%%")
			return "30%", nil
		}
		selector = "percentage"
	} else {
		if experimentsDetails.MemoryConsumptionPercentage == "0" {
			selector = "mebibytes"
		} else {
			log.Warn("Both MemoryConsumptionPercentage & MemoryConsumptionMebibytes provided as inputs, using the MemoryConsumptionPercentage value to proceed with the experiment")
			selector = "percentage"
		}
	}

	switch selector {

	case "percentage":

		//Getting the total memory under chaos
		memoryConsumptionPercentage, _ := strconv.ParseFloat(experimentsDetails.MemoryConsumptionPercentage, 64)
		memoryForChaos := (memoryConsumptionPercentage / 100) * float64(memoryCapacity)

		//Get the percentage of memory under chaos wrt allocatable memory
		totalMemoryConsumption = int((memoryForChaos / float64(memoryAllocatable)) * 100)
		if totalMemoryConsumption > 100 {
			log.Infof("[Info]: PercentageOfMemoryCapacity To Be Used: %v percent, which is more than 100 percent (%d percent) of Allocatable Memory, so the experiment will only consume upto 100 percent of Allocatable Memory", experimentsDetails.MemoryConsumptionPercentage, totalMemoryConsumption)
			MemoryConsumption = "100%"
		} else {
			log.Infof("[Info]: PercentageOfMemoryCapacity To Be Used: %v percent, which is %d percent of Allocatable Memory", experimentsDetails.MemoryConsumptionPercentage, totalMemoryConsumption)
			MemoryConsumption = strconv.Itoa(totalMemoryConsumption) + "%"
		}
		return MemoryConsumption, nil

	case "mebibytes":

		// Bringing all the values in Ki unit to compare
		// since 1Mi = 1025.390625Ki
		memoryConsumptionMebibytes, _ := strconv.ParseFloat(experimentsDetails.MemoryConsumptionMebibytes, 64)

		TotalMemoryConsumption := memoryConsumptionMebibytes * 1025.390625
		// since 1Ki = 1024 bytes
		memoryAllocatable := memoryAllocatable / 1024

		if memoryAllocatable < int(TotalMemoryConsumption) {
			MemoryConsumption = strconv.Itoa(memoryAllocatable) + "k"
			log.Infof("[Info]: The memory for consumption %vKi is more than the available memory %vKi, so the experiment will hog the memory upto %vKi", int(TotalMemoryConsumption), memoryAllocatable, memoryAllocatable)
		} else {
			MemoryConsumption = experimentsDetails.MemoryConsumptionMebibytes + "m"
		}
		return MemoryConsumption, nil
	}
	return "", cerrors.Error{ErrorCode: cerrors.ErrorTypeGeneric, Reason: "specify the memory consumption value either in percentage or mebibytes in a non-decimal format using respective envs"}
}

// createHelperPod derive the attributes for helper pod and create the helper pod
func createHelperPod(experimentsDetails *experimentTypes.ExperimentDetails, chaosDetails *types.ChaosDetails, appNode string, clients clients.ClientSets, MemoryConsumption string) error {

	terminationGracePeriodSeconds := int64(experimentsDetails.TerminationGracePeriodSeconds)

	helperPod := &apiv1.Pod{
		ObjectMeta: v1.ObjectMeta{
			GenerateName: experimentsDetails.ExperimentName + "-helper-",
			Namespace:    experimentsDetails.ChaosNamespace,
			Labels:       common.GetHelperLabels(chaosDetails.Labels, experimentsDetails.RunID, experimentsDetails.ExperimentName),
			Annotations:  chaosDetails.Annotations,
		},
		Spec: apiv1.PodSpec{
			RestartPolicy:                 apiv1.RestartPolicyNever,
			ImagePullSecrets:              chaosDetails.ImagePullSecrets,
			NodeName:                      appNode,
			TerminationGracePeriodSeconds: &terminationGracePeriodSeconds,
			Containers: []apiv1.Container{
				{
					Name:            experimentsDetails.ExperimentName,
					Image:           experimentsDetails.LIBImage,
					ImagePullPolicy: apiv1.PullPolicy(experimentsDetails.LIBImagePullPolicy),
					Command: []string{
						"stress-ng",
					},
					Args: []string{
						"--vm",
						experimentsDetails.NumberOfWorkers,
						"--vm-bytes",
						MemoryConsumption,
						"--timeout",
						strconv.Itoa(experimentsDetails.ChaosDuration) + "s",
					},
					Resources: chaosDetails.Resources,
				},
			},
		},
	}

	if len(chaosDetails.SideCar) != 0 {
		helperPod.Spec.Containers = append(helperPod.Spec.Containers, common.BuildSidecar(chaosDetails)...)
		helperPod.Spec.Volumes = append(helperPod.Spec.Volumes, common.GetSidecarVolumes(chaosDetails)...)
	}

	_, err := clients.KubeClient.CoreV1().Pods(experimentsDetails.ChaosNamespace).Create(context.Background(), helperPod, v1.CreateOptions{})
	if err != nil {
		return cerrors.Error{ErrorCode: cerrors.ErrorTypeGeneric, Reason: fmt.Sprintf("unable to create helper pod: %s", err.Error())}
	}
	return nil
}

// setChaosTunables will set up a random value within a given range of values
// If the value is not provided in range it'll set up the initial provided value.
func setChaosTunables(experimentsDetails *experimentTypes.ExperimentDetails) {
	experimentsDetails.MemoryConsumptionMebibytes = common.ValidateRange(experimentsDetails.MemoryConsumptionMebibytes)
	experimentsDetails.MemoryConsumptionPercentage = common.ValidateRange(experimentsDetails.MemoryConsumptionPercentage)
	experimentsDetails.NumberOfWorkers = common.ValidateRange(experimentsDetails.NumberOfWorkers)
	experimentsDetails.NodesAffectedPerc = common.ValidateRange(experimentsDetails.NodesAffectedPerc)
	experimentsDetails.Sequence = common.GetRandomSequence(experimentsDetails.Sequence)
}
