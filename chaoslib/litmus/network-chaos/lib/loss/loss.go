package loss

import (
	network_chaos "github.com/figwood/litmus-go/chaoslib/litmus/network-chaos/lib"
	clients "github.com/figwood/litmus-go/pkg/clients"
	experimentTypes "github.com/figwood/litmus-go/pkg/generic/network-chaos/types"
	"github.com/figwood/litmus-go/pkg/types"
)

// PodNetworkLossChaos contains the steps to prepare and inject chaos
func PodNetworkLossChaos(experimentsDetails *experimentTypes.ExperimentDetails, clients clients.ClientSets, resultDetails *types.ResultDetails, eventsDetails *types.EventDetails, chaosDetails *types.ChaosDetails) error {

	args := "loss " + experimentsDetails.NetworkPacketLossPercentage
	return network_chaos.PrepareAndInjectChaos(experimentsDetails, clients, resultDetails, eventsDetails, chaosDetails, args)
}
