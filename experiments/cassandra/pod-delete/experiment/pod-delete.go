package experiment

import (
	"os"

	"github.com/litmuschaos/chaos-operator/api/litmuschaos/v1alpha1"
	litmusLIB "github.com/figwood/litmus-go/chaoslib/litmus/pod-delete/lib"
	"github.com/figwood/litmus-go/pkg/cassandra"
	experimentEnv "github.com/figwood/litmus-go/pkg/cassandra/pod-delete/environment"
	experimentTypes "github.com/figwood/litmus-go/pkg/cassandra/pod-delete/types"
	clients "github.com/figwood/litmus-go/pkg/clients"
	"github.com/figwood/litmus-go/pkg/events"
	"github.com/figwood/litmus-go/pkg/log"
	"github.com/figwood/litmus-go/pkg/probe"
	"github.com/figwood/litmus-go/pkg/result"
	"github.com/figwood/litmus-go/pkg/status"
	"github.com/figwood/litmus-go/pkg/types"
	"github.com/figwood/litmus-go/pkg/utils/common"
	"github.com/sirupsen/logrus"
)

// CasssandraPodDelete inject the cassandra-pod-delete chaos
func CasssandraPodDelete(clients clients.ClientSets) {

	var err error
	var ResourceVersionBefore string
	experimentsDetails := experimentTypes.ExperimentDetails{}
	resultDetails := types.ResultDetails{}
	eventsDetails := types.EventDetails{}
	chaosDetails := types.ChaosDetails{}

	//Fetching all the ENV passed from the runner pod
	log.Infof("[PreReq]: Getting the ENV for the %v experiment", os.Getenv("EXPERIMENT_NAME"))
	experimentEnv.GetENV(&experimentsDetails)

	// Initialize the chaos attributes
	types.InitialiseChaosVariables(&chaosDetails)

	// Initialize Chaos Result Parameters
	types.SetResultAttributes(&resultDetails, chaosDetails)

	if experimentsDetails.ChaoslibDetail.EngineName != "" {
		// Get values from chaosengine. Bail out upon error, as we haven't entered exp business logic yet
		if err = types.GetValuesFromChaosEngine(&chaosDetails, clients, &resultDetails); err != nil {
			log.Errorf("Unable to initialize the probes, err: %v", err)
			return
		}
	}

	//Updating the chaos result in the beginning of experiment
	log.Infof("[PreReq]: Updating the chaos result of %v experiment (SOT)", experimentsDetails.ChaoslibDetail.ExperimentName)
	if err = result.ChaosResult(&chaosDetails, clients, &resultDetails, "SOT"); err != nil {
		log.Errorf("Unable to Create the Chaos Result, err: %v", err)
		result.RecordAfterFailure(&chaosDetails, &resultDetails, err, clients, &eventsDetails)
		return
	}

	// Set the chaos result uid
	result.SetResultUID(&resultDetails, clients, &chaosDetails)

	// generating the event in chaosresult to marked the verdict as awaited
	msg := "experiment: " + experimentsDetails.ChaoslibDetail.ExperimentName + ", Result: Awaited"
	types.SetResultEventAttributes(&eventsDetails, types.AwaitedVerdict, msg, "Normal", &resultDetails)
	events.GenerateEvents(&eventsDetails, clients, &chaosDetails, "ChaosResult")

	//DISPLAY THE APP INFORMATION
	log.InfoWithValues("The application informations are as follows", logrus.Fields{
		"Namespace":              experimentsDetails.ChaoslibDetail.AppNS,
		"Label":                  experimentsDetails.ChaoslibDetail.AppLabel,
		"CassandraLivenessImage": experimentsDetails.CassandraLivenessImage,
		"CassandraLivenessCheck": experimentsDetails.CassandraLivenessCheck,
		"CassandraPort":          experimentsDetails.CassandraPort,
	})

	// Calling AbortWatcher go routine, it will continuously watch for the abort signal and generate the required events and result
	go common.AbortWatcher(experimentsDetails.ChaoslibDetail.ExperimentName, clients, &resultDetails, &chaosDetails, &eventsDetails)

	//PRE-CHAOS APPLICATION STATUS CHECK
	if chaosDetails.DefaultHealthCheck {
		log.Info("[Status]: Verify that the AUT (Application Under Test) is running (pre-chaos)")
		if err = status.AUTStatusCheck(clients, &chaosDetails); err != nil {
			log.Errorf("Application status check failed, err: %v", err)
			types.SetEngineEventAttributes(&eventsDetails, types.PreChaosCheck, "AUT: Not Running", "Warning", &chaosDetails)
			events.GenerateEvents(&eventsDetails, clients, &chaosDetails, "ChaosEngine")
			result.RecordAfterFailure(&chaosDetails, &resultDetails, err, clients, &eventsDetails)
			return
		}

		// Checking the load distribution on the ring (pre-chaos)
		log.Info("[Status]: Checking the load distribution on the ring (pre-chaos)")
		if err = cassandra.NodeToolStatusCheck(&experimentsDetails, clients); err != nil {
			log.Errorf("[Status]: Chaos node tool status check failed, err: %v", err)
			result.RecordAfterFailure(&chaosDetails, &resultDetails, err, clients, &eventsDetails)
			return
		}
	}

	if experimentsDetails.ChaoslibDetail.EngineName != "" {
		// marking AUT as running, as we already checked the status of application under test
		msg := common.GetStatusMessage(chaosDetails.DefaultHealthCheck, "AUT: Running", "")

		// run the probes in the pre-chaos check
		if len(resultDetails.ProbeDetails) != 0 {

			if err = probe.RunProbes(&chaosDetails, clients, &resultDetails, "PreChaos", &eventsDetails); err != nil {
				log.Errorf("Probes Failed, err: %v", err)
				msg = common.GetStatusMessage(chaosDetails.DefaultHealthCheck, "AUT: Running", "Unsuccessful")
				types.SetEngineEventAttributes(&eventsDetails, types.PreChaosCheck, msg, "Warning", &chaosDetails)
				events.GenerateEvents(&eventsDetails, clients, &chaosDetails, "ChaosEngine")
				result.RecordAfterFailure(&chaosDetails, &resultDetails, err, clients, &eventsDetails)
				return
			}
			msg = common.GetStatusMessage(chaosDetails.DefaultHealthCheck, "AUT: Running", "Successful")
		}
		// generating the events for the pre-chaos check
		types.SetEngineEventAttributes(&eventsDetails, types.PreChaosCheck, msg, "Normal", &chaosDetails)
		events.GenerateEvents(&eventsDetails, clients, &chaosDetails, "ChaosEngine")
	}

	// Cassandra liveness check
	if experimentsDetails.CassandraLivenessCheck == "enable" {
		ResourceVersionBefore, err = cassandra.LivenessCheck(&experimentsDetails, clients)
		if err != nil {
			log.Errorf("[Liveness]: Cassandra liveness check failed, err: %v", err)
			result.RecordAfterFailure(&chaosDetails, &resultDetails, err, clients, &eventsDetails)
			return
		}
		log.Info("[Confirmation]: The cassandra application liveness pod created successfully")
	} else {
		log.Warn("[Liveness]: Cassandra Liveness check skipped as it was not enable")
	}

	chaosDetails.Phase = types.ChaosInjectPhase

	if err = litmusLIB.PreparePodDelete(experimentsDetails.ChaoslibDetail, clients, &resultDetails, &eventsDetails, &chaosDetails); err != nil {
		log.Errorf("Chaos injection failed, err: %v", err)
		result.RecordAfterFailure(&chaosDetails, &resultDetails, err, clients, &eventsDetails)
		return
	}

	log.Infof("[Confirmation]: %v chaos has been injected successfully", experimentsDetails.ChaoslibDetail.ExperimentName)
	resultDetails.Verdict = v1alpha1.ResultVerdictPassed

	chaosDetails.Phase = types.PostChaosPhase

	//POST-CHAOS APPLICATION STATUS CHECK
	if chaosDetails.DefaultHealthCheck {
		log.Info("[Status]: Verify that the AUT (Application Under Test) is running (post-chaos)")
		if err = status.AUTStatusCheck(clients, &chaosDetails); err != nil {
			log.Errorf("Application status check failed, err: %v", err)
			types.SetEngineEventAttributes(&eventsDetails, types.PostChaosCheck, "AUT: Not Running", "Warning", &chaosDetails)
			events.GenerateEvents(&eventsDetails, clients, &chaosDetails, "ChaosEngine")
			result.RecordAfterFailure(&chaosDetails, &resultDetails, err, clients, &eventsDetails)
			return
		}

		// Checking the load distribution on the ring (post-chaos)
		log.Info("[Status]: Checking the load distribution on the ring (post-chaos)")
		if err = cassandra.NodeToolStatusCheck(&experimentsDetails, clients); err != nil {
			log.Errorf("[Status]: Chaos node tool status check is failed, err: %v", err)
			result.RecordAfterFailure(&chaosDetails, &resultDetails, err, clients, &eventsDetails)
			return
		}
	}

	if experimentsDetails.ChaoslibDetail.EngineName != "" {
		// marking AUT as running, as we already checked the status of application under test
		msg := common.GetStatusMessage(chaosDetails.DefaultHealthCheck, "AUT: Running", "")

		// run the probes in the post-chaos check
		if len(resultDetails.ProbeDetails) != 0 {
			if err = probe.RunProbes(&chaosDetails, clients, &resultDetails, "PostChaos", &eventsDetails); err != nil {
				log.Errorf("Probes Failed, err: %v", err)
				msg = common.GetStatusMessage(chaosDetails.DefaultHealthCheck, "AUT: Running", "Unsuccessful")
				types.SetEngineEventAttributes(&eventsDetails, types.PostChaosCheck, msg, "Warning", &chaosDetails)
				events.GenerateEvents(&eventsDetails, clients, &chaosDetails, "ChaosEngine")
				result.RecordAfterFailure(&chaosDetails, &resultDetails, err, clients, &eventsDetails)
				return
			}
			msg = common.GetStatusMessage(chaosDetails.DefaultHealthCheck, "AUT: Running", "Successful")
		}

		// generating post chaos event
		types.SetEngineEventAttributes(&eventsDetails, types.PostChaosCheck, msg, "Normal", &chaosDetails)
		events.GenerateEvents(&eventsDetails, clients, &chaosDetails, "ChaosEngine")
	}

	// Cassandra statefulset liveness check (post-chaos)
	log.Info("[Status]: Confirm that the cassandra liveness pod is running(post-chaos)")
	// Checking the running status of cassandra liveness
	if experimentsDetails.CassandraLivenessCheck == "enable" {
		if err = status.CheckApplicationStatusesByLabels(experimentsDetails.ChaoslibDetail.AppNS, "name=cassandra-liveness-deploy-"+experimentsDetails.RunID, experimentsDetails.ChaoslibDetail.Timeout, experimentsDetails.ChaoslibDetail.Delay, clients); err != nil {
			log.Errorf("Liveness status check failed, err: %v", err)
			result.RecordAfterFailure(&chaosDetails, &resultDetails, err, clients, &eventsDetails)
			return
		}
		if err = cassandra.LivenessCleanup(&experimentsDetails, clients, ResourceVersionBefore); err != nil {
			log.Errorf("Liveness cleanup failed, err: %v", err)
			result.RecordAfterFailure(&chaosDetails, &resultDetails, err, clients, &eventsDetails)
			return
		}
	}
	//Updating the chaosResult in the end of experiment
	log.Info("[The End]: Updating the chaos result of cassandra pod delete experiment (EOT)")
	if err = result.ChaosResult(&chaosDetails, clients, &resultDetails, "EOT"); err != nil {
		log.Errorf("Unable to Update the Chaos Result, err: %v", err)
		return
	}

	// generating the event in chaosresult to marked the verdict as pass/fail
	msg = "experiment: " + experimentsDetails.ChaoslibDetail.ExperimentName + ", Result: " + string(resultDetails.Verdict)
	reason, eventType := types.GetChaosResultVerdictEvent(resultDetails.Verdict)
	types.SetResultEventAttributes(&eventsDetails, reason, msg, eventType, &resultDetails)
	events.GenerateEvents(&eventsDetails, clients, &chaosDetails, "ChaosResult")

	if experimentsDetails.ChaoslibDetail.EngineName != "" {
		msg := experimentsDetails.ChaoslibDetail.ExperimentName + " experiment has been " + string(resultDetails.Verdict) + "ed"
		types.SetEngineEventAttributes(&eventsDetails, types.Summary, msg, "Normal", &chaosDetails)
		events.GenerateEvents(&eventsDetails, clients, &chaosDetails, "ChaosEngine")
	}

}
