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

// PrepareEBSLossByID contains the prepration and injection steps for the experiment
func PrepareEBSLossByID(experimentsDetails *experimentTypes.ExperimentDetails, clients clients.ClientSets, resultDetails *types.ResultDetails, eventsDetails *types.EventDetails, chaosDetails *types.ChaosDetails) error {

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

		//get the volume id or list of instance ids
		volumeIDList := strings.Split(experimentsDetails.EBSVolumeID, ",")
		if len(volumeIDList) == 0 {
			return cerrors.Error{ErrorCode: cerrors.ErrorTypeTargetSelection, Reason: "no volume id found to detach"}
		}
		// watching for the abort signal and revert the chaos
		go ebsloss.AbortWatcher(experimentsDetails, volumeIDList, abort, chaosDetails)

		switch strings.ToLower(experimentsDetails.Sequence) {
		case "serial":
			if err = ebsloss.InjectChaosInSerialMode(experimentsDetails, volumeIDList, clients, resultDetails, eventsDetails, chaosDetails); err != nil {
				return stacktrace.Propagate(err, "could not run chaos in serial mode")
			}
		case "parallel":
			if err = ebsloss.InjectChaosInParallelMode(experimentsDetails, volumeIDList, clients, resultDetails, eventsDetails, chaosDetails); err != nil {
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
