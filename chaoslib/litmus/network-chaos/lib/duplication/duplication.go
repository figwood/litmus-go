package duplication

import (
	network_chaos "github.com/figwood/litmus-go/chaoslib/litmus/network-chaos/lib"
	clients "github.com/figwood/litmus-go/pkg/clients"
	experimentTypes "github.com/figwood/litmus-go/pkg/generic/network-chaos/types"
	"github.com/figwood/litmus-go/pkg/types"
)

// PodNetworkDuplicationChaos contains the steps to prepare and inject chaos
func PodNetworkDuplicationChaos(experimentsDetails *experimentTypes.ExperimentDetails, clients clients.ClientSets, resultDetails *types.ResultDetails, eventsDetails *types.EventDetails, chaosDetails *types.ChaosDetails) error {

	args := "duplicate " + experimentsDetails.NetworkPacketDuplicationPercentage
	return network_chaos.PrepareAndInjectChaos(experimentsDetails, clients, resultDetails, eventsDetails, chaosDetails, args)
}
