package latency

import (
	"strconv"

	network_chaos "github.com/figwood/litmus-go/chaoslib/litmus/network-chaos/lib"
	clients "github.com/figwood/litmus-go/pkg/clients"
	experimentTypes "github.com/figwood/litmus-go/pkg/generic/network-chaos/types"
	"github.com/figwood/litmus-go/pkg/types"
)

// PodNetworkLatencyChaos contains the steps to prepare and inject chaos
func PodNetworkLatencyChaos(experimentsDetails *experimentTypes.ExperimentDetails, clients clients.ClientSets, resultDetails *types.ResultDetails, eventsDetails *types.EventDetails, chaosDetails *types.ChaosDetails) error {

	args := "delay " + strconv.Itoa(experimentsDetails.NetworkLatency) + "ms " + strconv.Itoa(experimentsDetails.Jitter) + "ms"
	return network_chaos.PrepareAndInjectChaos(experimentsDetails, clients, resultDetails, eventsDetails, chaosDetails, args)
}
