package types

import (
	"fmt"

	errorsmod "cosmossdk.io/errors"
	math "cosmossdk.io/math"

	"github.com/dymensionxyz/gerr-cosmos/gerrc"

	sdk "github.com/cosmos/cosmos-sdk/types"
	dymnsutils "github.com/dymensionxyz/dymension/v3/x/dymns/utils"
)

// HasSetSellPrice returns true if the sell price is set
func (m *SellOrder) HasSetSellPrice() bool {
	return m.SellPrice != nil && !m.SellPrice.Amount.IsNil() && !m.SellPrice.IsZero()
}

// HasExpiredAtCtx returns true if the SO has expired at given context
func (m *SellOrder) HasExpiredAtCtx(ctx sdk.Context) bool {
	return m.HasExpired(ctx.BlockTime().Unix())
}

// HasExpired returns true if the SO has expired at given epoch
func (m *SellOrder) HasExpired(nowEpoch int64) bool {
	return m.ExpireAt < nowEpoch
}

// HasFinishedAtCtx returns true if the SO has expired or completed at given context
func (m *SellOrder) HasFinishedAtCtx(ctx sdk.Context) bool {
	return m.HasFinished(ctx.BlockTime().Unix())
}

// HasFinished returns true if the SO has expired or completed at given epoch
func (m *SellOrder) HasFinished(nowEpoch int64) bool {
	if m.HasExpired(nowEpoch) {
		return true
	}

	if !m.HasSetSellPrice() {
		// when no sell price is set, must wait until completed auction
		return false
	}

	// complete condition: bid >= sell price

	if m.HighestBid == nil {
		return false
	}

	return m.HighestBid.Price.IsGTE(*m.SellPrice)
}

// Validate performs basic validation for the SellOrder.
func (m *SellOrder) Validate() error {
	if m == nil {
		return errorsmod.Wrap(gerrc.ErrInvalidArgument, "SO is nil")
	}

	switch m.AssetType {
	case TypeName:
		if m.AssetId == "" {
			return errorsmod.Wrap(gerrc.ErrInvalidArgument, "Dym-Name of SO is empty")
		}

		if !dymnsutils.IsValidDymName(m.AssetId) {
			return errorsmod.Wrap(gerrc.ErrInvalidArgument, "Dym-Name of SO is not a valid dym name")
		}
	case TypeAlias:
		if m.AssetId == "" {
			return errorsmod.Wrap(gerrc.ErrInvalidArgument, "alias of SO is empty")
		}

		if !dymnsutils.IsValidAlias(m.AssetId) {
			return errorsmod.Wrap(gerrc.ErrInvalidArgument, "alias of SO is not a valid alias")
		}
	default:
		return errorsmod.Wrapf(gerrc.ErrInvalidArgument, "invalid SO type: %s", m.AssetType)
	}

	if m.ExpireAt == 0 {
		return errorsmod.Wrap(gerrc.ErrInvalidArgument, "SO expiry is empty")
	}

	if m.MinPrice.Amount.IsNil() || m.MinPrice.IsZero() {
		return errorsmod.Wrap(gerrc.ErrInvalidArgument, "SO min price is zero")
	} else if m.MinPrice.IsNegative() {
		return errorsmod.Wrap(gerrc.ErrInvalidArgument, "SO min price is negative")
	} else if err := m.MinPrice.Validate(); err != nil {
		return errorsmod.Wrapf(gerrc.ErrInvalidArgument, "SO min price is invalid: %v", err)
	}

	if m.HasSetSellPrice() {
		if m.SellPrice.IsNegative() {
			return errorsmod.Wrap(gerrc.ErrInvalidArgument, "SO sell price is negative")
		} else if err := m.SellPrice.Validate(); err != nil {
			return errorsmod.Wrapf(gerrc.ErrInvalidArgument, "SO sell price is invalid: %v", err)
		}

		if m.SellPrice.Denom != m.MinPrice.Denom {
			return errorsmod.Wrap(gerrc.ErrInvalidArgument, "SO sell price denom is different from min price denom")
		}

		if m.SellPrice.IsLT(m.MinPrice) {
			return errorsmod.Wrap(gerrc.ErrInvalidArgument, "SO sell price is less than min price")
		}
	}

	if m.HighestBid == nil {
		// valid, means no bid yet
	} else if err := m.HighestBid.Validate(m.AssetType); err != nil {
		return errorsmod.Wrapf(gerrc.ErrInvalidArgument, "SO highest bid is invalid: %v", err)
	} else if m.HighestBid.Price.IsLT(m.MinPrice) {
		return errorsmod.Wrap(gerrc.ErrInvalidArgument, "SO highest bid price is less than min price")
	} else if m.HasSetSellPrice() && m.SellPrice.IsLT(m.HighestBid.Price) {
		return errorsmod.Wrap(gerrc.ErrInvalidArgument, "SO sell price is less than highest bid price")
	}

	return nil
}

// Validate performs basic validation for the SellOrderBid.
func (m *SellOrderBid) Validate(assetType AssetType) error {
	if m == nil {
		return errorsmod.Wrap(gerrc.ErrInvalidArgument, "SO bid is nil")
	}

	if m.Bidder == "" {
		return errorsmod.Wrap(gerrc.ErrInvalidArgument, "SO bidder is empty")
	}

	if !dymnsutils.IsValidBech32AccountAddress(m.Bidder, true) {
		return errorsmod.Wrap(gerrc.ErrInvalidArgument, "SO bidder is not a valid bech32 account address")
	}

	if m.Price.Amount.IsNil() || m.Price.IsZero() {
		return errorsmod.Wrap(gerrc.ErrInvalidArgument, "SO bid price is zero")
	} else if m.Price.IsNegative() {
		return errorsmod.Wrap(gerrc.ErrInvalidArgument, "SO bid price is negative")
	} else if err := m.Price.Validate(); err != nil {
		return errorsmod.Wrapf(gerrc.ErrInvalidArgument, "SO bid price is invalid: %v", err)
	}

	if err := ValidateOrderParams(m.Params, assetType); err != nil {
		return err
	}

	return nil
}

// GetSdkEvent returns the sdk event contains information of Sell-Order record.
// Fired when Sell-Order record is set into store.
func (m SellOrder) GetSdkEvent(actionName string) sdk.Event {
	var sellPrice sdk.Coin
	if m.HasSetSellPrice() {
		sellPrice = *m.SellPrice
	} else {
		sellPrice = sdk.NewCoin(m.MinPrice.Denom, math.ZeroInt())
	}

	var attrHighestBidder, attrHighestBidPrice sdk.Attribute
	if m.HighestBid != nil {
		attrHighestBidder = sdk.NewAttribute(AttributeKeySoHighestBidder, m.HighestBid.Bidder)
		attrHighestBidPrice = sdk.NewAttribute(AttributeKeySoHighestBidPrice, m.HighestBid.Price.String())
	} else {
		attrHighestBidder = sdk.NewAttribute(AttributeKeySoHighestBidder, "")
		attrHighestBidPrice = sdk.NewAttribute(AttributeKeySoHighestBidPrice, sdk.NewCoin(m.MinPrice.Denom, math.ZeroInt()).String())
	}

	return sdk.NewEvent(
		EventTypeSellOrder,
		sdk.NewAttribute(AttributeKeySoAssetId, m.AssetId),
		sdk.NewAttribute(AttributeKeySoAssetType, m.AssetType.PrettyName()),
		sdk.NewAttribute(AttributeKeySoExpiryEpoch, fmt.Sprintf("%d", m.ExpireAt)),
		sdk.NewAttribute(AttributeKeySoMinPrice, m.MinPrice.String()),
		sdk.NewAttribute(AttributeKeySoSellPrice, sellPrice.String()),
		attrHighestBidder,
		attrHighestBidPrice,
		sdk.NewAttribute(AttributeKeySoActionName, actionName),
	)
}
