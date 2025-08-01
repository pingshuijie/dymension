package types_test

import (
	"testing"

	transfertypes "github.com/cosmos/ibc-go/v8/modules/apps/transfer/types"
	channeltypes "github.com/cosmos/ibc-go/v8/modules/core/04-channel/types"
	"github.com/stretchr/testify/require"

	commontypes "github.com/dymensionxyz/dymension/v3/x/common/types"
)

func TestEncodeDecodePacketKey(t *testing.T) {
	packet := commontypes.RollappPacket{
		RollappId:   "rollapp_1234-1",
		Status:      commontypes.Status_PENDING,
		ProofHeight: 8,
		Packet:      getNewTestPacket(t),
	}

	expectedPK := packet.RollappPacketKey()

	encoded := commontypes.EncodePacketKey(expectedPK)
	decoded, err := commontypes.DecodePacketKey(encoded)
	require.NoError(t, err)

	require.Equal(t, expectedPK, decoded)
}

func getNewTestPacket(t *testing.T) *channeltypes.Packet {
	t.Helper()
	data := &transfertypes.FungibleTokenPacketData{
		Receiver: "testReceiver",
	}
	pd, err := transfertypes.ModuleCdc.MarshalJSON(data)
	require.NoError(t, err)
	return &channeltypes.Packet{
		SourcePort:         "testSourcePort",
		SourceChannel:      "testSourceChannel",
		DestinationPort:    "testDestinationPort",
		DestinationChannel: "testDestinationChannel",
		Data:               pd,
		Sequence:           1,
	}
}
