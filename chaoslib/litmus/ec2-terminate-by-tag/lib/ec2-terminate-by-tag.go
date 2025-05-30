package lib

import (
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/figwood/litmus-go/pkg/cerrors"
	clients "github.com/figwood/litmus-go/pkg/clients"
	awslib "github.com/figwood/litmus-go/pkg/cloud/aws/ec2"
	"github.com/figwood/litmus-go/pkg/events"
	experimentTypes "github.com/figwood/litmus-go/pkg/kube-aws/ec2-terminate-by-tag/types"
	"github.com/figwood/litmus-go/pkg/log"
	"github.com/figwood/litmus-go/pkg/probe"
	"github.com/figwood/litmus-go/pkg/types"
	"github.com/figwood/litmus-go/pkg/utils/common"
	"github.com/palantir/stacktrace"
	"github.com/sirupsen/logrus"
)

var inject, abort chan os.Signal

// PrepareEC2TerminateByTag contains the prepration and injection steps for the experiment
func PrepareEC2TerminateByTag(experimentsDetails *experimentTypes.ExperimentDetails, clients clients.ClientSets, resultDetails *types.ResultDetails, eventsDetails *types.EventDetails, chaosDetails *types.ChaosDetails) error {

	// inject channel is used to transmit signal notifications.
	inject = make(chan os.Signal, 1)
	// Catch and relay certain signal(s) to inject channel.
	signal.Notify(inject, os.Interrupt, syscall.SIGTERM)

	// abort channel is used to transmit signal notifications.
	abort = make(chan os.Signal, 1)
	// Catch and relay certain signal(s) to abort channel.
	signal.Notify(abort, os.Interrupt, syscall.SIGTERM)

	//Waiting for the ramp time before chaos injection
	if experimentsDetails.RampTime != 0 {
		log.Infof("[Ramp]: Waiting for the %vs ramp time before injecting chaos", experimentsDetails.RampTime)
		common.WaitForDuration(experimentsDetails.RampTime)
	}

	instanceIDList := common.FilterBasedOnPercentage(experimentsDetails.InstanceAffectedPerc, experimentsDetails.TargetInstanceIDList)
	log.Infof("[Chaos]:Number of Instance targeted: %v", len(instanceIDList))

	// watching for the abort signal and revert the chaos
	go abortWatcher(experimentsDetails, instanceIDList, chaosDetails)

	switch strings.ToLower(experimentsDetails.Sequence) {
	case "serial":
		if err := injectChaosInSerialMode(experimentsDetails, instanceIDList, clients, resultDetails, eventsDetails, chaosDetails); err != nil {
			return stacktrace.Propagate(err, "could not run chaos in serial mode")
		}
	case "parallel":
		if err := injectChaosInParallelMode(experimentsDetails, instanceIDList, clients, resultDetails, eventsDetails, chaosDetails); err != nil {
			return stacktrace.Propagate(err, "could not run chaos in parallel mode")
		}
	default:
		return cerrors.Error{ErrorCode: cerrors.ErrorTypeTargetSelection, Reason: fmt.Sprintf("'%s' sequence is not supported", experimentsDetails.Sequence)}
	}

	//Waiting for the ramp time after chaos injection
	if experimentsDetails.RampTime != 0 {
		log.Infof("[Ramp]: Waiting for the %vs ramp time after injecting chaos", experimentsDetails.RampTime)
		common.WaitForDuration(experimentsDetails.RampTime)
	}
	return nil
}

// injectChaosInSerialMode will inject the ce2 instance termination in serial mode that is one after other
func injectChaosInSerialMode(experimentsDetails *experimentTypes.ExperimentDetails, instanceIDList []string, clients clients.ClientSets, resultDetails *types.ResultDetails, eventsDetails *types.EventDetails, chaosDetails *types.ChaosDetails) error {

	select {
	case <-inject:
		// stopping the chaos execution, if abort signal received
		os.Exit(0)
	default:
		//ChaosStartTimeStamp contains the start timestamp, when the chaos injection begin
		ChaosStartTimeStamp := time.Now()
		duration := int(time.Since(ChaosStartTimeStamp).Seconds())

		for duration < experimentsDetails.ChaosDuration {

			log.Infof("[Info]: Target instanceID list, %v", instanceIDList)

			if experimentsDetails.EngineName != "" {
				msg := "Injecting " + experimentsDetails.ExperimentName + " chaos on ec2 instance"
				types.SetEngineEventAttributes(eventsDetails, types.ChaosInject, msg, "Normal", chaosDetails)
				events.GenerateEvents(eventsDetails, clients, chaosDetails, "ChaosEngine")
			}

			//PowerOff the instance
			for i, id := range instanceIDList {

				//Stopping the EC2 instance
				log.Info("[Chaos]: Stopping the desired EC2 instance")
				if err := awslib.EC2Stop(id, experimentsDetails.Region); err != nil {
					return stacktrace.Propagate(err, "ec2 instance failed to stop")
				}

				common.SetTargets(id, "injected", "EC2", chaosDetails)

				//Wait for ec2 instance to completely stop
				log.Infof("[Wait]: Wait for EC2 instance '%v' to get in stopped state", id)
				if err := awslib.WaitForEC2Down(experimentsDetails.Timeout, experimentsDetails.Delay, experimentsDetails.ManagedNodegroup, experimentsDetails.Region, id); err != nil {
					return stacktrace.Propagate(err, "ec2 instance failed to stop")
				}

				// run the probes during chaos
				// the OnChaos probes execution will start in the first iteration and keep running for the entire chaos duration
				if len(resultDetails.ProbeDetails) != 0 && i == 0 {
					if err := probe.RunProbes(chaosDetails, clients, resultDetails, "DuringChaos", eventsDetails); err != nil {
						return stacktrace.Propagate(err, "failed to run probes")
					}
				}

				//Wait for chaos interval
				log.Infof("[Wait]: Waiting for chaos interval of %vs", experimentsDetails.ChaosInterval)
				time.Sleep(time.Duration(experimentsDetails.ChaosInterval) * time.Second)

				//Starting the EC2 instance
				if experimentsDetails.ManagedNodegroup != "enable" {
					log.Info("[Chaos]: Starting back the EC2 instance")
					if err := awslib.EC2Start(id, experimentsDetails.Region); err != nil {
						return stacktrace.Propagate(err, "ec2 instance failed to start")
					}

					//Wait for ec2 instance to get in running state
					log.Infof("[Wait]: Wait for EC2 instance '%v' to get in running state", id)
					if err := awslib.WaitForEC2Up(experimentsDetails.Timeout, experimentsDetails.Delay, experimentsDetails.ManagedNodegroup, experimentsDetails.Region, id); err != nil {
						return stacktrace.Propagate(err, "ec2 instance failed to start")
					}
				}
				common.SetTargets(id, "reverted", "EC2", chaosDetails)
			}
			duration = int(time.Since(ChaosStartTimeStamp).Seconds())
		}
	}
	return nil
}

// injectChaosInParallelMode will inject the ce2 instance termination in parallel mode that is all at once
func injectChaosInParallelMode(experimentsDetails *experimentTypes.ExperimentDetails, instanceIDList []string, clients clients.ClientSets, resultDetails *types.ResultDetails, eventsDetails *types.EventDetails, chaosDetails *types.ChaosDetails) error {

	select {
	case <-inject:
		// stopping the chaos execution, if abort signal received
		os.Exit(0)
	default:
		//ChaosStartTimeStamp contains the start timestamp, when the chaos injection begin
		ChaosStartTimeStamp := time.Now()
		duration := int(time.Since(ChaosStartTimeStamp).Seconds())

		for duration < experimentsDetails.ChaosDuration {
			log.Infof("[Info]: Target instanceID list, %v", instanceIDList)

			if experimentsDetails.EngineName != "" {
				msg := "Injecting " + experimentsDetails.ExperimentName + " chaos on ec2 instance"
				types.SetEngineEventAttributes(eventsDetails, types.ChaosInject, msg, "Normal", chaosDetails)
				events.GenerateEvents(eventsDetails, clients, chaosDetails, "ChaosEngine")
			}

			//PowerOff the instance
			for _, id := range instanceIDList {
				//Stopping the EC2 instance
				log.Info("[Chaos]: Stopping the desired EC2 instance")
				if err := awslib.EC2Stop(id, experimentsDetails.Region); err != nil {
					return stacktrace.Propagate(err, "ec2 instance failed to stop")
				}
				common.SetTargets(id, "injected", "EC2", chaosDetails)
			}

			for _, id := range instanceIDList {
				//Wait for ec2 instance to completely stop
				log.Infof("[Wait]: Wait for EC2 instance '%v' to get in stopped state", id)
				if err := awslib.WaitForEC2Down(experimentsDetails.Timeout, experimentsDetails.Delay, experimentsDetails.ManagedNodegroup, experimentsDetails.Region, id); err != nil {
					return stacktrace.Propagate(err, "ec2 instance failed to stop")
				}
			}

			// run the probes during chaos
			if len(resultDetails.ProbeDetails) != 0 {
				if err := probe.RunProbes(chaosDetails, clients, resultDetails, "DuringChaos", eventsDetails); err != nil {
					return stacktrace.Propagate(err, "failed to run probes")
				}
			}

			//Wait for chaos interval
			log.Infof("[Wait]: Waiting for chaos interval of %vs", experimentsDetails.ChaosInterval)
			time.Sleep(time.Duration(experimentsDetails.ChaosInterval) * time.Second)

			//Starting the EC2 instance
			if experimentsDetails.ManagedNodegroup != "enable" {

				for _, id := range instanceIDList {
					log.Info("[Chaos]: Starting back the EC2 instance")
					if err := awslib.EC2Start(id, experimentsDetails.Region); err != nil {
						return stacktrace.Propagate(err, "ec2 instance failed to start")
					}
				}

				for _, id := range instanceIDList {
					//Wait for ec2 instance to get in running state
					log.Infof("[Wait]: Wait for EC2 instance '%v' to get in running state", id)
					if err := awslib.WaitForEC2Up(experimentsDetails.Timeout, experimentsDetails.Delay, experimentsDetails.ManagedNodegroup, experimentsDetails.Region, id); err != nil {
						return stacktrace.Propagate(err, "ec2 instance failed to start")
					}
				}
			}
			for _, id := range instanceIDList {
				common.SetTargets(id, "reverted", "EC2", chaosDetails)
			}
			duration = int(time.Since(ChaosStartTimeStamp).Seconds())
		}
	}
	return nil
}

// SetTargetInstance will select the target instance which are in running state and filtered from the given instance tag
func SetTargetInstance(experimentsDetails *experimentTypes.ExperimentDetails) error {

	instanceIDList, err := awslib.GetInstanceList(experimentsDetails.InstanceTag, experimentsDetails.Region)
	if err != nil {
		return stacktrace.Propagate(err, "failed to get the instance id list")
	}
	if len(instanceIDList) == 0 {
		return cerrors.Error{
			ErrorCode: cerrors.ErrorTypeTargetSelection,
			Reason:    fmt.Sprintf("no instance found with the given tag %v, in region %v", experimentsDetails.InstanceTag, experimentsDetails.Region),
		}
	}

	for _, id := range instanceIDList {
		instanceState, err := awslib.GetEC2InstanceStatus(id, experimentsDetails.Region)
		if err != nil {
			return stacktrace.Propagate(err, "failed to get the instance status while selecting the target instances")
		}
		if instanceState == "running" {
			experimentsDetails.TargetInstanceIDList = append(experimentsDetails.TargetInstanceIDList, id)
		}
	}

	if len(experimentsDetails.TargetInstanceIDList) == 0 {
		return cerrors.Error{
			ErrorCode: cerrors.ErrorTypeChaosInject,
			Reason:    "failed to get any running instance",
			Target:    fmt.Sprintf("EC2 Instance Tag: %v", experimentsDetails.InstanceTag)}
	}

	log.InfoWithValues("[Info]: Targeting the running instances filtered from instance tag", logrus.Fields{
		"Total number of instances filtered":   len(instanceIDList),
		"Number of running instances filtered": len(experimentsDetails.TargetInstanceIDList),
	})
	return nil
}

// watching for the abort signal and revert the chaos
func abortWatcher(experimentsDetails *experimentTypes.ExperimentDetails, instanceIDList []string, chaosDetails *types.ChaosDetails) {

	<-abort

	log.Info("[Abort]: Chaos Revert Started")
	for _, id := range instanceIDList {
		instanceState, err := awslib.GetEC2InstanceStatus(id, experimentsDetails.Region)
		if err != nil {
			log.Errorf("Failed to get instance status when an abort signal is received: %v", err)
		}
		if instanceState != "running" && experimentsDetails.ManagedNodegroup != "enable" {

			log.Info("[Abort]: Waiting for the EC2 instance to get down")
			if err := awslib.WaitForEC2Down(experimentsDetails.Timeout, experimentsDetails.Delay, experimentsDetails.ManagedNodegroup, experimentsDetails.Region, id); err != nil {
				log.Errorf("Unable to wait till stop of the instance: %v", err)
			}

			log.Info("[Abort]: Starting EC2 instance as abort signal received")
			err := awslib.EC2Start(id, experimentsDetails.Region)
			if err != nil {
				log.Errorf("EC2 instance failed to start when an abort signal is received: %v", err)
			}
		}
		common.SetTargets(id, "reverted", "EC2", chaosDetails)
	}
	log.Info("[Abort]: Chaos Revert Completed")
	os.Exit(1)
}
