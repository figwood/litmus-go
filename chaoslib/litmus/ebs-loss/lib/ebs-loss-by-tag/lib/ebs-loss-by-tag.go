package lib

import (
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	ebsloss "github.com/figwood/litmus-go/chaoslib/litmus/ebs-loss/lib"
	"github.com/figwood/litmus-go/pkg/cerrors"
	clients "github.com/figwood/litmus-go/pkg/clients"
	experimentTypes "github.com/figwood/litmus-go/pkg/kube-aws/ebs-loss/types"
	"github.com/figwood/litmus-go/pkg/log"
	"github.com/figwood/litmus-go/pkg/types"
	"github.com/figwood/litmus-go/pkg/utils/common"
	"github.com/palantir/stacktrace"
)

var (
	err           error
	inject, abort chan os.Signal
)

// PrepareEBSLossByTag contains the prepration and injection steps for the experiment
func PrepareEBSLossByTag(experimentsDetails *experimentTypes.ExperimentDetails, clients clients.ClientSets, resultDetails *types.ResultDetails, eventsDetails *types.EventDetails, chaosDetails *types.ChaosDetails) error {

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

	select {
	case <-inject:
		// stopping the chaos execution, if abort signal received
		os.Exit(0)
	default:

		targetEBSVolumeIDList := common.FilterBasedOnPercentage(experimentsDetails.VolumeAffectedPerc, experimentsDetails.TargetVolumeIDList)
		log.Infof("[Chaos]:Number of volumes targeted: %v", len(targetEBSVolumeIDList))

		// watching for the abort signal and revert the chaos
		go ebsloss.AbortWatcher(experimentsDetails, targetEBSVolumeIDList, abort, chaosDetails)

		switch strings.ToLower(experimentsDetails.Sequence) {
		case "serial":
			if err = ebsloss.InjectChaosInSerialMode(experimentsDetails, targetEBSVolumeIDList, clients, resultDetails, eventsDetails, chaosDetails); err != nil {
				return stacktrace.Propagate(err, "could not run chaos in serial mode")
			}
		case "parallel":
			if err = ebsloss.InjectChaosInParallelMode(experimentsDetails, targetEBSVolumeIDList, clients, resultDetails, eventsDetails, chaosDetails); err != nil {
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
	}
	return nil
}
