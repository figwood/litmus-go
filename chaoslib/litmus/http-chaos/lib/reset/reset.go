package reset

import (
	"strconv"

	http_chaos "github.com/figwood/litmus-go/chaoslib/litmus/http-chaos/lib"
	clients "github.com/figwood/litmus-go/pkg/clients"
	experimentTypes "github.com/figwood/litmus-go/pkg/generic/http-chaos/types"
	"github.com/figwood/litmus-go/pkg/log"
	"github.com/figwood/litmus-go/pkg/types"
	"github.com/sirupsen/logrus"
)

// PodHttpResetPeerChaos contains the steps to prepare and inject http reset peer chaos
func PodHttpResetPeerChaos(experimentsDetails *experimentTypes.ExperimentDetails, clients clients.ClientSets, resultDetails *types.ResultDetails, eventsDetails *types.EventDetails, chaosDetails *types.ChaosDetails) error {

	log.InfoWithValues("[Info]: The chaos tunables are:", logrus.Fields{
		"Target Port":      experimentsDetails.TargetServicePort,
		"Listen Port":      experimentsDetails.ProxyPort,
		"Sequence":         experimentsDetails.Sequence,
		"PodsAffectedPerc": experimentsDetails.PodsAffectedPerc,
		"Toxicity":         experimentsDetails.Toxicity,
		"Reset Timeout":    experimentsDetails.ResetTimeout,
	})

	args := "-t reset_peer -a timeout=" + strconv.Itoa(experimentsDetails.ResetTimeout)
	return http_chaos.PrepareAndInjectChaos(experimentsDetails, clients, resultDetails, eventsDetails, chaosDetails, args)
}
