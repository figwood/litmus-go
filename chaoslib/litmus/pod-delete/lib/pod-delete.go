package lib

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/figwood/litmus-go/pkg/cerrors"
	"github.com/figwood/litmus-go/pkg/workloads"
	"github.com/palantir/stacktrace"

	clients "github.com/figwood/litmus-go/pkg/clients"
	"github.com/figwood/litmus-go/pkg/events"
	experimentTypes "github.com/figwood/litmus-go/pkg/generic/pod-delete/types"
	"github.com/figwood/litmus-go/pkg/log"
	"github.com/figwood/litmus-go/pkg/probe"
	"github.com/figwood/litmus-go/pkg/status"
	"github.com/figwood/litmus-go/pkg/types"
	"github.com/figwood/litmus-go/pkg/utils/common"
	"github.com/sirupsen/logrus"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PreparePodDelete contains the prepration steps before chaos injection
func PreparePodDelete(experimentsDetails *experimentTypes.ExperimentDetails, clients clients.ClientSets, resultDetails *types.ResultDetails, eventsDetails *types.EventDetails, chaosDetails *types.ChaosDetails) error {

	//Waiting for the ramp time before chaos injection
	if experimentsDetails.RampTime != 0 {
		log.Infof("[Ramp]: Waiting for the %vs ramp time before injecting chaos", experimentsDetails.RampTime)
		common.WaitForDuration(experimentsDetails.RampTime)
	}

	//set up the tunables if provided in range
	SetChaosTunables(experimentsDetails)

	log.InfoWithValues("[Info]: The chaos tunables are:", logrus.Fields{
		"PodsAffectedPerc": experimentsDetails.PodsAffectedPerc,
		"Sequence":         experimentsDetails.Sequence,
	})

	switch strings.ToLower(experimentsDetails.Sequence) {
	case "serial":
		if err := injectChaosInSerialMode(experimentsDetails, clients, chaosDetails, eventsDetails, resultDetails); err != nil {
			return stacktrace.Propagate(err, "could not run chaos in serial mode")
		}
	case "parallel":
		if err := injectChaosInParallelMode(experimentsDetails, clients, chaosDetails, eventsDetails, resultDetails); err != nil {
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

// injectChaosInSerialMode delete the target application pods serial mode(one by one)
func injectChaosInSerialMode(experimentsDetails *experimentTypes.ExperimentDetails, clients clients.ClientSets, chaosDetails *types.ChaosDetails, eventsDetails *types.EventDetails, resultDetails *types.ResultDetails) error {

	// run the probes during chaos
	if len(resultDetails.ProbeDetails) != 0 {
		if err := probe.RunProbes(chaosDetails, clients, resultDetails, "DuringChaos", eventsDetails); err != nil {
			return err
		}
	}

	GracePeriod := int64(0)
	//ChaosStartTimeStamp contains the start timestamp, when the chaos injection begin
	ChaosStartTimeStamp := time.Now()
	duration := int(time.Since(ChaosStartTimeStamp).Seconds())

	for duration < experimentsDetails.ChaosDuration {
		// Get the target pod details for the chaos execution
		// if the target pod is not defined it will derive the random target pod list using pod affected percentage
		if experimentsDetails.TargetPods == "" && chaosDetails.AppDetail == nil {
			return cerrors.Error{ErrorCode: cerrors.ErrorTypeTargetSelection, Reason: "provide one of the appLabel or TARGET_PODS"}
		}

		targetPodList, err := common.GetTargetPods(experimentsDetails.NodeLabel, experimentsDetails.TargetPods, experimentsDetails.PodsAffectedPerc, clients, chaosDetails)
		if err != nil {
			return stacktrace.Propagate(err, "could not get target pods")
		}

		// deriving the parent name of the target resources
		for _, pod := range targetPodList.Items {
			kind, parentName, err := workloads.GetPodOwnerTypeAndName(&pod, clients.DynamicClient)
			if err != nil {
				return stacktrace.Propagate(err, "could not get pod owner name and kind")
			}
			common.SetParentName(parentName, kind, pod.Namespace, chaosDetails)
		}
		for _, target := range chaosDetails.ParentsResources {
			common.SetTargets(target.Name, "targeted", target.Kind, chaosDetails)
		}

		if experimentsDetails.EngineName != "" {
			msg := "Injecting " + experimentsDetails.ExperimentName + " chaos on application pod"
			types.SetEngineEventAttributes(eventsDetails, types.ChaosInject, msg, "Normal", chaosDetails)
			events.GenerateEvents(eventsDetails, clients, chaosDetails, "ChaosEngine")
		}

		//Deleting the application pod
		for _, pod := range targetPodList.Items {

			log.InfoWithValues("[Info]: Killing the following pods", logrus.Fields{
				"PodName": pod.Name})

			if experimentsDetails.Force {
				err = clients.KubeClient.CoreV1().Pods(pod.Namespace).Delete(context.Background(), pod.Name, v1.DeleteOptions{GracePeriodSeconds: &GracePeriod})
			} else {
				err = clients.KubeClient.CoreV1().Pods(pod.Namespace).Delete(context.Background(), pod.Name, v1.DeleteOptions{})
			}
			if err != nil {
				return cerrors.Error{ErrorCode: cerrors.ErrorTypeChaosInject, Target: fmt.Sprintf("{podName: %s, namespace: %s}", pod.Name, pod.Namespace), Reason: fmt.Sprintf("failed to delete the target pod: %s", err.Error())}
			}

			switch chaosDetails.Randomness {
			case true:
				if err := common.RandomInterval(experimentsDetails.ChaosInterval); err != nil {
					return stacktrace.Propagate(err, "could not get random chaos interval")
				}
			default:
				//Waiting for the chaos interval after chaos injection
				if experimentsDetails.ChaosInterval != "" {
					log.Infof("[Wait]: Wait for the chaos interval %vs", experimentsDetails.ChaosInterval)
					waitTime, _ := strconv.Atoi(experimentsDetails.ChaosInterval)
					common.WaitForDuration(waitTime)
				}
			}

			//Verify the status of pod after the chaos injection
			log.Info("[Status]: Verification for the recreation of application pod")
			for _, parent := range chaosDetails.ParentsResources {
				target := types.AppDetails{
					Names:     []string{parent.Name},
					Kind:      parent.Kind,
					Namespace: parent.Namespace,
				}
				if err = status.CheckUnTerminatedPodStatusesByWorkloadName(target, experimentsDetails.Timeout, experimentsDetails.Delay, clients); err != nil {
					return stacktrace.Propagate(err, "could not check pod statuses by workload names")
				}
			}

			duration = int(time.Since(ChaosStartTimeStamp).Seconds())
		}

	}
	log.Infof("[Completion]: %v chaos is done", experimentsDetails.ExperimentName)

	return nil

}

// injectChaosInParallelMode delete the target application pods in parallel mode (all at once)
func injectChaosInParallelMode(experimentsDetails *experimentTypes.ExperimentDetails, clients clients.ClientSets, chaosDetails *types.ChaosDetails, eventsDetails *types.EventDetails, resultDetails *types.ResultDetails) error {

	// run the probes during chaos
	if len(resultDetails.ProbeDetails) != 0 {
		if err := probe.RunProbes(chaosDetails, clients, resultDetails, "DuringChaos", eventsDetails); err != nil {
			return err
		}
	}

	GracePeriod := int64(0)
	//ChaosStartTimeStamp contains the start timestamp, when the chaos injection begin
	ChaosStartTimeStamp := time.Now()
	duration := int(time.Since(ChaosStartTimeStamp).Seconds())

	for duration < experimentsDetails.ChaosDuration {
		// Get the target pod details for the chaos execution
		// if the target pod is not defined it will derive the random target pod list using pod affected percentage
		if experimentsDetails.TargetPods == "" && chaosDetails.AppDetail == nil {
			return cerrors.Error{ErrorCode: cerrors.ErrorTypeTargetSelection, Reason: "please provide one of the appLabel or TARGET_PODS"}
		}
		targetPodList, err := common.GetTargetPods(experimentsDetails.NodeLabel, experimentsDetails.TargetPods, experimentsDetails.PodsAffectedPerc, clients, chaosDetails)
		if err != nil {
			return stacktrace.Propagate(err, "could not get target pods")
		}

		// deriving the parent name of the target resources
		for _, pod := range targetPodList.Items {
			kind, parentName, err := workloads.GetPodOwnerTypeAndName(&pod, clients.DynamicClient)
			if err != nil {
				return stacktrace.Propagate(err, "could not get pod owner name and kind")
			}
			common.SetParentName(parentName, kind, pod.Namespace, chaosDetails)
		}
		for _, target := range chaosDetails.ParentsResources {
			common.SetTargets(target.Name, "targeted", target.Kind, chaosDetails)
		}

		if experimentsDetails.EngineName != "" {
			msg := "Injecting " + experimentsDetails.ExperimentName + " chaos on application pod"
			types.SetEngineEventAttributes(eventsDetails, types.ChaosInject, msg, "Normal", chaosDetails)
			events.GenerateEvents(eventsDetails, clients, chaosDetails, "ChaosEngine")
		}

		//Deleting the application pod
		for _, pod := range targetPodList.Items {

			log.InfoWithValues("[Info]: Killing the following pods", logrus.Fields{
				"PodName": pod.Name})

			if experimentsDetails.Force {
				err = clients.KubeClient.CoreV1().Pods(pod.Namespace).Delete(context.Background(), pod.Name, v1.DeleteOptions{GracePeriodSeconds: &GracePeriod})
			} else {
				err = clients.KubeClient.CoreV1().Pods(pod.Namespace).Delete(context.Background(), pod.Name, v1.DeleteOptions{})
			}
			if err != nil {
				return cerrors.Error{ErrorCode: cerrors.ErrorTypeChaosInject, Target: fmt.Sprintf("{podName: %s, namespace: %s}", pod.Name, pod.Namespace), Reason: fmt.Sprintf("failed to delete the target pod: %s", err.Error())}
			}
		}

		switch chaosDetails.Randomness {
		case true:
			if err := common.RandomInterval(experimentsDetails.ChaosInterval); err != nil {
				return stacktrace.Propagate(err, "could not get random chaos interval")
			}
		default:
			//Waiting for the chaos interval after chaos injection
			if experimentsDetails.ChaosInterval != "" {
				log.Infof("[Wait]: Wait for the chaos interval %vs", experimentsDetails.ChaosInterval)
				waitTime, _ := strconv.Atoi(experimentsDetails.ChaosInterval)
				common.WaitForDuration(waitTime)
			}
		}

		//Verify the status of pod after the chaos injection
		log.Info("[Status]: Verification for the recreation of application pod")
		for _, parent := range chaosDetails.ParentsResources {
			target := types.AppDetails{
				Names:     []string{parent.Name},
				Kind:      parent.Kind,
				Namespace: parent.Namespace,
			}
			if err = status.CheckUnTerminatedPodStatusesByWorkloadName(target, experimentsDetails.Timeout, experimentsDetails.Delay, clients); err != nil {
				return stacktrace.Propagate(err, "could not check pod statuses by workload names")
			}
		}
		duration = int(time.Since(ChaosStartTimeStamp).Seconds())
	}

	log.Infof("[Completion]: %v chaos is done", experimentsDetails.ExperimentName)

	return nil
}

// SetChaosTunables will setup a random value within a given range of values
// If the value is not provided in range it'll setup the initial provided value.
func SetChaosTunables(experimentsDetails *experimentTypes.ExperimentDetails) {
	experimentsDetails.PodsAffectedPerc = common.ValidateRange(experimentsDetails.PodsAffectedPerc)
	experimentsDetails.Sequence = common.GetRandomSequence(experimentsDetails.Sequence)
}
