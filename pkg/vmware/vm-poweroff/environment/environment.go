package environment

import (
	"strconv"
	"strings"

	clientTypes "k8s.io/apimachinery/pkg/types"

	"github.com/figwood/litmus-go/pkg/types"
	experimentTypes "github.com/figwood/litmus-go/pkg/vmware/vm-poweroff/types"
)

// GetENV fetches all the env variables from the runner pod
func GetENV(experimentDetails *experimentTypes.ExperimentDetails) {
	experimentDetails.ExperimentName = types.Getenv("EXPERIMENT_NAME", "vm-poweroff")
	experimentDetails.ChaosNamespace = types.Getenv("CHAOS_NAMESPACE", "litmus")
	experimentDetails.EngineName = types.Getenv("CHAOSENGINE", "")
	experimentDetails.ChaosDuration, _ = strconv.Atoi(types.Getenv("TOTAL_CHAOS_DURATION", "30"))
	experimentDetails.ChaosInterval, _ = strconv.Atoi(types.Getenv("CHAOS_INTERVAL", "30"))
	experimentDetails.RampTime, _ = strconv.Atoi(types.Getenv("RAMP_TIME", ""))
	experimentDetails.ChaosUID = clientTypes.UID(types.Getenv("CHAOS_UID", ""))
	experimentDetails.InstanceID = types.Getenv("INSTANCE_ID", "")
	experimentDetails.ChaosPodName = types.Getenv("POD_NAME", "")
	experimentDetails.Delay, _ = strconv.Atoi(types.Getenv("STATUS_CHECK_DELAY", "2"))
	experimentDetails.Timeout, _ = strconv.Atoi(types.Getenv("STATUS_CHECK_TIMEOUT", "180"))
	experimentDetails.Sequence = types.Getenv("SEQUENCE", "parallel")
	experimentDetails.VMIds = strings.TrimSpace(types.Getenv("APP_VM_MOIDS", ""))
	experimentDetails.VMTag = strings.TrimSpace(types.Getenv("APP_VM_TAG", ""))
	experimentDetails.VcenterServer = types.Getenv("VCENTERSERVER", "")
	experimentDetails.VcenterUser = types.Getenv("VCENTERUSER", "")
	experimentDetails.VcenterPass = types.Getenv("VCENTERPASS", "")
}
