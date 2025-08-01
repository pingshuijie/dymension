package ibctesting_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	transfertypes "github.com/cosmos/ibc-go/v8/modules/apps/transfer/types"
	clienttypes "github.com/cosmos/ibc-go/v8/modules/core/02-client/types"
	channeltypes "github.com/cosmos/ibc-go/v8/modules/core/04-channel/types"
	ibctesting "github.com/cosmos/ibc-go/v8/testing"

	"github.com/dymensionxyz/dymension/v3/app/apptesting"
	appparams "github.com/dymensionxyz/dymension/v3/app/params"
	"github.com/dymensionxyz/dymension/v3/testutil/sample"
	irotypes "github.com/dymensionxyz/dymension/v3/x/iro/types"
	rollapptypes "github.com/dymensionxyz/dymension/v3/x/rollapp/types"
)

var successAck = channeltypes.CommitAcknowledgement(channeltypes.NewResultAcknowledgement([]byte{byte(1)}).Acknowledgement())

type GenesisBridgeSuite struct {
	ibcTestingSuite
	path *ibctesting.Path
}

func TestGenesisBridgeTestSuite(t *testing.T) {
	suite.Run(t, new(GenesisBridgeSuite))
}

func (s *GenesisBridgeSuite) SetupTest() {
	s.ibcTestingSuite.SetupTest()
	s.hubApp().LightClientKeeper.SetEnabled(false) // disable state validation against light client

	path := s.newTransferPath(s.hubChain(), s.rollappChain())
	s.coordinator.Setup(path)
	s.path = path

	s.createRollapp(false, nil) // genesis protocol is not finished yet

	// force set the canonical client for the rollapp
	s.setRollappLightClientID(rollappChainID(), s.path.EndpointA.ClientID)

	// set hooks to avoid actually creating VFC contract, as this places extra requirements on the test setup
	// we assume that if the denom metadata was created (checked below), then the hooks ran correctly
	s.hubApp().DenomMetadataKeeper.SetHooks(nil)
}

// TestHappyPath_NoGenesisAccounts tests a valid genesis info with no genesis accounts
func (s *GenesisBridgeSuite) TestHappyPath_NoGenesisAccounts() {
	// create the expected genesis bridge packet
	rollapp := s.hubApp().RollappKeeper.MustGetRollapp(s.hubCtx(), rollappChainID())
	s.registerSequencer()

	// send the packet on the rollapp chain
	packet := s.genesisBridgePacket(rollapp.GenesisInfo)
	seq, err := s.path.EndpointB.SendPacket(packet.TimeoutHeight, packet.TimeoutTimestamp, packet.Data)
	s.Require().NoError(err)
	packet.Sequence = seq

	// submit rollapp's state update
	s.updateRollappState(uint64(s.rollappChain().App.LastBlockHeight())) //nolint:gosec

	_, err = s.path.EndpointA.RecvPacketWithResult(packet)
	s.Require().NoError(err)

	// assert the ack succeeded
	ack, found := s.hubApp().IBCKeeper.ChannelKeeper.GetPacketAcknowledgement(s.hubCtx(), packet.GetDestPort(), packet.GetDestChannel(), packet.GetSequence())
	s.Require().True(found)
	s.Require().Equal(successAck, ack)

	rollapp = s.hubApp().RollappKeeper.MustGetRollapp(s.hubCtx(), rollappChainID())
	// assert the transfers are enabled
	s.Require().True(rollapp.GenesisState.IsTransferEnabled())

	// assert denom registered
	expectedIBCdenom := transfertypes.ParseDenomTrace(transfertypes.GetPrefixedDenom(s.path.EndpointB.ChannelConfig.PortID, s.path.EndpointB.ChannelID, rollapp.GenesisInfo.NativeDenom.Base)).IBCDenom()
	metadata, found := s.hubApp().BankKeeper.GetDenomMetaData(s.hubCtx(), expectedIBCdenom)
	s.Require().True(found)
	s.Require().Equal(rollapp.GenesisInfo.NativeDenom.Display, metadata.Display)
}

// TestHappyPath_GenesisAccounts tests a valid genesis info with genesis accounts
func (s *GenesisBridgeSuite) TestHappyPath_GenesisAccounts() {
	gAddr := s.rollappChain().SenderAccount.GetAddress()
	gAccounts := []rollapptypes.GenesisAccount{
		{
			Address: gAddr.String(),
			Amount:  math.NewIntFromUint64(100000),
		},
	}
	s.addGenesisAccounts(gAccounts)
	s.registerSequencer()

	// create the expected genesis bridge packet
	rollapp := s.hubApp().RollappKeeper.MustGetRollapp(s.hubCtx(), rollappChainID())
	packet := s.genesisBridgePacket(rollapp.GenesisInfo)

	// send the packet on the rollapp chain
	seq, err := s.path.EndpointB.SendPacket(packet.TimeoutHeight, packet.TimeoutTimestamp, packet.Data)
	s.Require().NoError(err)
	packet.Sequence = seq

	// submit rollapp's state update
	s.updateRollappState(uint64(s.rollappChain().App.LastBlockHeight())) //nolint:gosec

	_, err = s.path.EndpointA.RecvPacketWithResult(packet)
	s.Require().NoError(err)

	// assert the ack succeeded
	ack, found := s.hubApp().IBCKeeper.ChannelKeeper.GetPacketAcknowledgement(s.hubCtx(), packet.GetDestPort(), packet.GetDestChannel(), packet.GetSequence())
	s.Require().True(found)
	s.Require().Equal(successAck, ack)

	// assert the genesis accounts were funded
	ibcDenom := transfertypes.ParseDenomTrace(transfertypes.GetPrefixedDenom(s.path.EndpointB.ChannelConfig.PortID, s.path.EndpointB.ChannelID, rollapp.GenesisInfo.NativeDenom.Base)).IBCDenom()
	balance := s.hubApp().BankKeeper.GetBalance(s.hubCtx(), gAddr, ibcDenom)
	s.Require().Equal(gAccounts[0].Amount, balance.Amount)
}

// TestIRO tests a valid genesis info with genesis accounts, including IRO plan
// We expect the IRO plan to be settled once the genesis bridge is completed
func (s *GenesisBridgeSuite) TestIRO() {
	// fund the rollapp owner account for iro creation fee
	iroFee := sdk.NewCoin(appparams.BaseDenom, s.hubApp().IROKeeper.GetParams(s.hubCtx()).CreationFee)
	apptesting.FundAccount(s.hubApp(), s.hubCtx(), s.hubChain().SenderAccount.GetAddress(), sdk.NewCoins(iroFee))

	amt := math.NewIntFromUint64(1_000_000).MulRaw(1e18)

	// Add the iro module to the genesis accounts
	gAddr := s.hubApp().IROKeeper.GetModuleAccountAddress()
	gAccounts := []rollapptypes.GenesisAccount{
		{
			Address: gAddr,
			Amount:  amt,
		},
	}
	s.addGenesisAccounts(gAccounts)

	// create IRO plan
	rollapp := s.hubApp().RollappKeeper.MustGetRollapp(s.hubCtx(), rollappChainID())
	_, err := s.hubApp().IROKeeper.CreatePlan(s.hubCtx(), "adym", amt, 0, time.Time{}, true, rollapp, irotypes.DefaultBondingCurve(), irotypes.DefaultIncentivePlanParams(), irotypes.DefaultParams().MinLiquidityPart, time.Hour, 0)
	s.Require().NoError(err)

	// register the sequencer
	s.registerSequencer()

	// create the expected genesis bridge packet
	rollapp = s.hubApp().RollappKeeper.MustGetRollapp(s.hubCtx(), rollappChainID())
	packet := s.genesisBridgePacket(rollapp.GenesisInfo)

	// send the packet on the rollapp chain
	seq, err := s.path.EndpointB.SendPacket(packet.TimeoutHeight, packet.TimeoutTimestamp, packet.Data)
	s.Require().NoError(err)
	packet.Sequence = seq

	// submit rollapp's state update
	s.updateRollappState(uint64(s.rollappChain().App.LastBlockHeight())) //nolint:gosec

	_, err = s.path.EndpointA.RecvPacketWithResult(packet)
	s.Require().NoError(err)

	// assert the ack succeeded
	ack, found := s.hubApp().IBCKeeper.ChannelKeeper.GetPacketAcknowledgement(s.hubCtx(), packet.GetDestPort(), packet.GetDestChannel(), packet.GetSequence())
	s.Require().True(found)
	s.Require().Equal(successAck, ack)

	// assert the transfers are enabled
	rollapp = s.hubApp().RollappKeeper.MustGetRollapp(s.hubCtx(), rollappChainID())
	s.Require().True(rollapp.GenesisState.IsTransferEnabled())

	// the iro plan should be settled
	plan, found := s.hubApp().IROKeeper.GetPlanByRollapp(s.hubCtx(), rollappChainID())
	s.Require().True(found)
	expectedIBCdenom := transfertypes.ParseDenomTrace(transfertypes.GetPrefixedDenom(s.path.EndpointB.ChannelConfig.PortID, s.path.EndpointB.ChannelID, rollapp.GenesisInfo.NativeDenom.Base)).IBCDenom()
	s.Require().Equal(plan.SettledDenom, expectedIBCdenom)
}

// TestInvalidGenesisInfo tests an invalid genesis info
func (s *GenesisBridgeSuite) TestInvalidGenesisInfo() {
	rollapp := s.hubApp().RollappKeeper.MustGetRollapp(s.hubCtx(), rollappChainID())
	s.registerSequencer()

	// invalid native denom
	gInfoCopy := rollapp.GenesisInfo
	gInfoCopy.NativeDenom.Base = "wrong"
	packet := s.genesisBridgePacket(gInfoCopy)

	// send the packet on the rollapp chain and assert the ack failed
	seq, err := s.path.EndpointB.SendPacket(packet.TimeoutHeight, packet.TimeoutTimestamp, packet.Data)
	s.Require().NoError(err)
	packet.Sequence = seq

	// submit rollapp's state update
	s.updateRollappState(uint64(s.rollappChain().App.LastBlockHeight())) //nolint:gosec

	_, err = s.path.EndpointA.RecvPacketWithResult(packet)
	s.Require().NoError(err)
	ack, found := s.hubApp().IBCKeeper.ChannelKeeper.GetPacketAcknowledgement(s.hubCtx(), packet.GetDestPort(), packet.GetDestChannel(), packet.GetSequence())
	s.Require().True(found)
	s.Require().NotEqual(successAck, ack)

	// invalid initial supply
	gInfoCopy = rollapp.GenesisInfo
	gInfoCopy.InitialSupply = math.NewInt(53453)
	packet = s.genesisBridgePacket(gInfoCopy)

	// send the packet on the rollapp chain and assert the ack failed
	seq, err = s.path.EndpointB.SendPacket(packet.TimeoutHeight, packet.TimeoutTimestamp, packet.Data)
	s.Require().NoError(err)
	packet.Sequence = seq

	// submit rollapp's state update
	s.updateRollappState(uint64(s.rollappChain().App.LastBlockHeight())) //nolint:gosec

	_, err = s.path.EndpointA.RecvPacketWithResult(packet)
	s.Require().NoError(err)
	ack, found = s.hubApp().IBCKeeper.ChannelKeeper.GetPacketAcknowledgement(s.hubCtx(), packet.GetDestPort(), packet.GetDestChannel(), packet.GetSequence())
	s.Require().True(found)
	s.Require().NotEqual(successAck, ack)

	// wrong GenesisAccounts
	gInfoCopy = rollapp.GenesisInfo
	gInfoCopy.GenesisAccounts = &rollapptypes.GenesisAccounts{
		Accounts: []rollapptypes.GenesisAccount{
			{
				Address: sample.AccAddress(),
				Amount:  math.NewIntFromUint64(10000000000000000000),
			},
		},
	}
	packet = s.genesisBridgePacket(gInfoCopy)

	// send the packet on the rollapp chain and assert the ack failed
	seq, err = s.path.EndpointB.SendPacket(packet.TimeoutHeight, packet.TimeoutTimestamp, packet.Data)
	s.Require().NoError(err)
	packet.Sequence = seq

	// submit rollapp's state update
	s.updateRollappState(uint64(s.rollappChain().App.LastBlockHeight())) //nolint:gosec

	_, err = s.path.EndpointA.RecvPacketWithResult(packet)
	s.Require().NoError(err)
	ack, found = s.hubApp().IBCKeeper.ChannelKeeper.GetPacketAcknowledgement(s.hubCtx(), packet.GetDestPort(), packet.GetDestChannel(), packet.GetSequence())
	s.Require().True(found)
	s.Require().NotEqual(successAck, ack)
}

// TestInvalidGenesisDenomMetadata tests an invalid genesis denom metadata
func (s *GenesisBridgeSuite) TestInvalidGenesisDenomMetadata() {
	rollapp := s.hubApp().RollappKeeper.MustGetRollapp(s.hubCtx(), rollappChainID())
	s.registerSequencer()

	packet := s.genesisBridgePacket(rollapp.GenesisInfo)
	var gb rollapptypes.GenesisBridgeData
	err := json.Unmarshal(packet.Data, &gb)
	s.Require().NoError(err)

	// change the base denom in the metadata
	gb.NativeDenom.Base = "wrong"
	gb.NativeDenom.DenomUnits[0].Denom = "wrong"
	packet.Data, err = gb.Marshal()
	s.Require().NoError(err)

	// send the packet on the rollapp chain and assert the ack failed
	seq, err := s.path.EndpointB.SendPacket(packet.TimeoutHeight, packet.TimeoutTimestamp, packet.Data)
	s.Require().NoError(err)
	packet.Sequence = seq

	// submit rollapp's state update
	s.updateRollappState(uint64(s.rollappChain().App.LastBlockHeight())) //nolint:gosec

	_, err = s.path.EndpointA.RecvPacketWithResult(packet)
	s.Require().NoError(err)
	ack, found := s.hubApp().IBCKeeper.ChannelKeeper.GetPacketAcknowledgement(s.hubCtx(), packet.GetDestPort(), packet.GetDestChannel(), packet.GetSequence())
	s.Require().True(found)
	s.Require().NotEqual(successAck, ack)

	// assert the original packet does work
	packet = s.genesisBridgePacket(rollapp.GenesisInfo)
	seq, err = s.path.EndpointB.SendPacket(packet.TimeoutHeight, packet.TimeoutTimestamp, packet.Data)
	s.Require().NoError(err)
	packet.Sequence = seq

	// submit rollapp's state update
	s.updateRollappState(uint64(s.rollappChain().App.LastBlockHeight())) //nolint:gosec

	_, err = s.path.EndpointA.RecvPacketWithResult(packet)
	s.Require().NoError(err)
	ack, found = s.hubApp().IBCKeeper.ChannelKeeper.GetPacketAcknowledgement(s.hubCtx(), packet.GetDestPort(), packet.GetDestChannel(), packet.GetSequence())
	s.Require().True(found)
	s.Require().Equal(successAck, ack)
}

// TestInvalidGenesisTransfer tests an invalid genesis transfer
func (s *GenesisBridgeSuite) TestInvalidGenesisTransfer() {
	s.addGenesisAccounts([]rollapptypes.GenesisAccount{
		{
			Address: sample.AccAddress(),
			Amount:  math.NewIntFromUint64(10000000000000000000),
		},
	})
	rollapp := s.hubApp().RollappKeeper.MustGetRollapp(s.hubCtx(), rollappChainID())
	s.registerSequencer()

	packet := s.genesisBridgePacket(rollapp.GenesisInfo)

	// change the amount in the genesis transfer
	var gb rollapptypes.GenesisBridgeData
	err := json.Unmarshal(packet.Data, &gb)
	s.Require().NoError(err)
	gb.GenesisTransfer.Amount = "1242353645"
	packet.Data, err = gb.Marshal()
	s.Require().NoError(err)

	// send the packet on the rollapp chain and assert the ack failed
	seq, err := s.path.EndpointB.SendPacket(packet.TimeoutHeight, packet.TimeoutTimestamp, packet.Data)
	s.Require().NoError(err)
	packet.Sequence = seq

	// submit rollapp's state update
	s.updateRollappState(uint64(s.rollappChain().App.LastBlockHeight())) //nolint:gosec

	_, err = s.path.EndpointA.RecvPacketWithResult(packet)
	s.Require().NoError(err)
	ack, found := s.hubApp().IBCKeeper.ChannelKeeper.GetPacketAcknowledgement(s.hubCtx(), packet.GetDestPort(), packet.GetDestChannel(), packet.GetSequence())
	s.Require().True(found)
	s.Require().NotEqual(successAck, ack)

	// assert the original packet does work
	packet = s.genesisBridgePacket(rollapp.GenesisInfo)
	seq, err = s.path.EndpointB.SendPacket(packet.TimeoutHeight, packet.TimeoutTimestamp, packet.Data)
	s.Require().NoError(err)
	packet.Sequence = seq

	// submit rollapp's state update
	s.updateRollappState(uint64(s.rollappChain().App.LastBlockHeight())) //nolint:gosec

	_, err = s.path.EndpointA.RecvPacketWithResult(packet)
	s.Require().NoError(err)
	ack, found = s.hubApp().IBCKeeper.ChannelKeeper.GetPacketAcknowledgement(s.hubCtx(), packet.GetDestPort(), packet.GetDestChannel(), packet.GetSequence())
	s.Require().True(found)
	s.Require().Equal(successAck, ack)
}

// TestBridgeDisabledEnabled tests that the bridge is disabled until the genesis bridge is completed
// after the genesis bridge is completed, the bridge should be enabled
func (s *GenesisBridgeSuite) TestBridgeDisabledEnabled() {
	s.registerSequencer()
	amt := math.NewIntFromUint64(10000000000000000000)
	denom := "foo"
	coin := sdk.NewCoin(denom, amt)
	apptesting.FundAccount(s.rollappApp(), s.rollappCtx(), s.rollappChain().SenderAccount.GetAddress(), sdk.NewCoins(coin))

	msg := s.transferMsg(amt, denom)
	res, err := s.rollappChain().SendMsgs(msg)
	s.Require().NoError(err)
	packet, err := ibctesting.ParsePacketFromEvents(res.GetEvents())
	s.Require().NoError(err)

	err = s.path.RelayPacket(packet)
	s.Require().NoError(err)

	ack, found := s.hubApp().IBCKeeper.ChannelKeeper.GetPacketAcknowledgement(s.hubCtx(), packet.GetDestPort(), packet.GetDestChannel(), packet.GetSequence())
	s.Require().True(found)
	s.Require().NotEqual(successAck, ack) // assert for ack error

	// create the expected genesis bridge packet
	rollapp := s.hubApp().RollappKeeper.MustGetRollapp(s.hubCtx(), rollappChainID())
	packet = s.genesisBridgePacket(rollapp.GenesisInfo)

	// send the packet on the rollapp chain
	seq, err := s.path.EndpointB.SendPacket(packet.TimeoutHeight, packet.TimeoutTimestamp, packet.Data)
	s.Require().NoError(err)
	packet.Sequence = seq

	// submit rollapp's state update
	s.updateRollappState(uint64(s.rollappChain().App.LastBlockHeight())) //nolint:gosec

	_, err = s.path.EndpointA.RecvPacketWithResult(packet)
	s.Require().NoError(err)

	rollapp = s.hubApp().RollappKeeper.MustGetRollapp(s.hubCtx(), rollappChainID())
	s.Require().True(rollapp.GenesisState.IsTransferEnabled())
	s.Require().Equal(rollapp.ChannelId, packet.GetDestChannel())

	// assert the ack succeeded
	ack, found = s.hubApp().IBCKeeper.ChannelKeeper.GetPacketAcknowledgement(s.hubCtx(), packet.GetDestPort(), packet.GetDestChannel(), packet.GetSequence())
	s.Require().True(found)
	s.Require().Equal(successAck, ack)

	// assert the transfers are enabled
	rollapp = s.hubApp().RollappKeeper.MustGetRollapp(s.hubCtx(), rollappChainID())
	s.Require().True(rollapp.GenesisState.IsTransferEnabled())

	// assert the transfer now goes through
	res, err = s.rollappChain().SendMsgs(msg)
	s.Require().NoError(err)
	packet, err = ibctesting.ParsePacketFromEvents(res.GetEvents())
	s.Require().NoError(err)

	err = s.path.RelayPacket(packet)
	s.Require().Error(err) // expecting error as no AcknowledgePacket expected to return
}

/* ---------------------------------- utils --------------------------------- */
// genesisBridgePacket creates a genesis bridge packet with the given parameters
func (s *GenesisBridgeSuite) genesisBridgePacket(raGenesisInfo rollapptypes.GenesisInfo) channeltypes.Packet {
	denom := raGenesisInfo.NativeDenom.Base
	display := raGenesisInfo.NativeDenom.Display
	initialSupply := raGenesisInfo.InitialSupply

	var gb rollapptypes.GenesisBridgeData

	meta := banktypes.Metadata{
		DenomUnits: []*banktypes.DenomUnit{
			{
				Denom: denom,
			},
			{
				Denom:    display,
				Exponent: 18,
			},
		},
		Base:    denom,
		Display: display,
		Name:    denom,
		Symbol:  display,
	}
	s.Require().NoError(meta.Validate()) // sanity check the test is written correctly

	gb.GenesisInfo = rollapptypes.GenesisBridgeInfo{
		GenesisChecksum: "checksum",
		Bech32Prefix:    "ethm",
		NativeDenom: rollapptypes.DenomMetadata{
			Base:     meta.Base,
			Display:  meta.DenomUnits[1].Denom,
			Exponent: meta.DenomUnits[1].Exponent,
		},
		InitialSupply:   initialSupply,
		GenesisAccounts: []rollapptypes.GenesisAccount{},
	}
	gb.NativeDenom = meta

	// add genesis transfer if needed
	if raGenesisInfo.RequiresTransfer() {

		gTransfer := transfertypes.NewFungibleTokenPacketData(
			denom,
			raGenesisInfo.GenesisTransferAmount().String(),
			s.rollappChain().SenderAccount.GetAddress().String(),
			rollapptypes.HubRecipient,
			"",
		)
		gb.GenesisTransfer = &gTransfer
		gb.GenesisInfo.GenesisAccounts = raGenesisInfo.Accounts()
	}

	bz, err := json.Marshal(gb)
	s.Require().NoError(err)

	msg := channeltypes.NewPacket(
		bz,
		0, // will be set after the submission
		s.path.EndpointB.ChannelConfig.PortID,
		s.path.EndpointB.ChannelID,
		s.path.EndpointA.ChannelConfig.PortID,
		s.path.EndpointA.ChannelID,
		clienttypes.ZeroHeight(),
		uint64(s.hubCtx().BlockTime().Add(10*time.Minute).UnixNano()), //nolint:gosec
	)

	return msg
}

func (s *GenesisBridgeSuite) transferMsg(amt math.Int, denom string) *transfertypes.MsgTransfer {
	msg := transfertypes.NewMsgTransfer(
		s.path.EndpointB.ChannelConfig.PortID,
		s.path.EndpointB.ChannelID,
		sdk.NewCoin(denom, amt),
		s.rollappChain().SenderAccount.GetAddress().String(),
		s.hubChain().SenderAccount.GetAddress().String(),
		clienttypes.ZeroHeight(),
		uint64(s.hubCtx().BlockTime().Add(10*time.Minute).UnixNano()), //nolint:gosec
		"",
	)

	return msg
}

// method to update the rollapp genesis info
func (s *GenesisBridgeSuite) addGenesisAccounts(genesisAccounts []rollapptypes.GenesisAccount) {
	rollapp := s.hubApp().RollappKeeper.MustGetRollapp(s.hubCtx(), rollappChainID())

	if rollapp.GenesisInfo.GenesisAccounts == nil {
		rollapp.GenesisInfo.GenesisAccounts = &rollapptypes.GenesisAccounts{}
	}
	rollapp.GenesisInfo.GenesisAccounts.Accounts = append(rollapp.GenesisInfo.Accounts(), genesisAccounts...)
	s.hubApp().RollappKeeper.SetRollapp(s.hubCtx(), rollapp)
}
