package keeper

import (
	"context"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	abci "github.com/cometbft/cometbft/abci/types"

	"github.com/andromedaprotocol/andromedad/x/distribution/types"
)

// AllocateTokens performs reward and fee distribution to all validators based
// on the F1 fee distribution specification.
func (k Keeper) AllocateTokens(ctx context.Context, totalPreviousPower int64, bondedVotes []abci.VoteInfo) error {
	// fetch and clear the collected fees for distribution, since this is
	// called in BeginBlock, collected fees will be from the previous block
	// (and distributed to the previous proposer)
	feeCollector := k.authKeeper.GetModuleAccount(ctx, k.feeCollectorName)
	feesCollectedInt := k.bankKeeper.GetAllBalances(ctx, feeCollector.GetAddress())
	feesCollected := sdk.NewDecCoinsFromCoins(feesCollectedInt...)

	// Fetch the RewardsDripper module account
	rewardsDripper := k.authKeeper.GetModuleAccount(ctx, types.RewardsDripperName)
	// Fetch the rewards dripper balance
	rewardsDripperBalance := k.bankKeeper.GetAllBalances(ctx, rewardsDripper.GetAddress())
	// Convert rewardsDripperBalance to DecCoins
	rewardsDripperCollected := sdk.NewDecCoinsFromCoins(rewardsDripperBalance...)

	// transfer collected fees to the distribution module account
	err := k.bankKeeper.SendCoinsFromModuleToModule(ctx, k.feeCollectorName, types.ModuleName, feesCollectedInt)
	if err != nil {
		return err
	}

	// Calculate rewards to be dripped this block from Param set
	rewardsToDrip, err := k.GetRewardsPerBlock(ctx)
	if err != nil {
		return err
	}

	// If rewardsToDrip is nil set to 0
	if rewardsToDrip.IsNil() {
		rewardsToDrip = math.LegacyZeroDec()
	}
	// Create new coins with the denoms of the rewardsDripperBalance and the amount of rewards to be dripped
	rewardsCoins := make(sdk.Coins, len(rewardsDripperBalance))
	for i, coin := range rewardsDripperBalance {
		rewardsCoins[i] = sdk.NewCoin(coin.Denom, rewardsToDrip.TruncateInt())
	}

	// Convert to DecCoins
	rewardsToDripDec := sdk.NewDecCoinsFromCoins(rewardsCoins...)

	// Intersect balance of rewardsDripper with rewardsToDripDec to find the amount to be dripped
	rewardsToDripDec = rewardsToDripDec.Intersect(rewardsDripperCollected)

	// Convert rewardsToDripDec to Coins
	rewardsToDripInt, _ := rewardsToDripDec.TruncateDecimal()

	// transfer rewards to be dripped to the distribution module account
	if err := k.bankKeeper.SendCoinsFromModuleToModule(ctx, types.RewardsDripperName, types.ModuleName, rewardsToDripInt); err != nil {
		panic(err)
	}

	// temporary workaround to keep CanWithdrawInvariant happy
	// general discussions here: https://github.com/cosmos/cosmos-sdk/issues/2906#issuecomment-441867634
	feePool, err := k.FeePool.Get(ctx)
	if err != nil {
		return err
	}

	if totalPreviousPower == 0 {
		feePool.CommunityPool = feePool.CommunityPool.Add(feesCollected...)
		return k.FeePool.Set(ctx, feePool)
	}

	///

	// Combine all rewards
	allCollected := feesCollected.Add(rewardsToDripDec...)
	// calculate fraction allocated to validators
	remaining := allCollected

	// calculate fraction allocated to validators
	communityTax, err := k.GetCommunityTax(ctx)
	if err != nil {
		return err
	}

	voteMultiplier := math.LegacyOneDec().Sub(communityTax)
	feeMultiplier := feesCollected.MulDecTruncate(voteMultiplier)

	// To avoid adding a community tax to rewards to be dripped we add the rewardsToDripDec to the feeMultiplier
	// We DO NOT want to re-tax funds that already come from the pool as these are basely rewards
	feeMultiplier = feeMultiplier.Add(rewardsToDripDec...)

	// allocate tokens proportionally to voting power
	//
	// TODO: Consider parallelizing later
	//
	// Ref: https://github.com/cosmos/cosmos-sdk/pull/3099#discussion_r246276376
	for _, vote := range bondedVotes {
		validator, err := k.stakingKeeper.ValidatorByConsAddr(ctx, vote.Validator.Address)
		if err != nil {
			return err
		}

		// TODO: Consider micro-slashing for missing votes.
		//
		// Ref: https://github.com/cosmos/cosmos-sdk/issues/2525#issuecomment-430838701
		powerFraction := math.LegacyNewDec(vote.Validator.Power).QuoTruncate(math.LegacyNewDec(totalPreviousPower))
		reward := feeMultiplier.MulDecTruncate(powerFraction)

		err = k.AllocateTokensToValidator(ctx, validator, reward)
		if err != nil {
			return err
		}

		remaining = remaining.Sub(reward)
	}

	// allocate community funding
	feePool.CommunityPool = feePool.CommunityPool.Add(remaining...)
	return k.FeePool.Set(ctx, feePool)
}

// AllocateTokensToValidator allocate tokens to a particular validator,
// splitting according to commission.
func (k Keeper) AllocateTokensToValidator(ctx context.Context, val stakingtypes.ValidatorI, tokens sdk.DecCoins) error {
	// split tokens between validator and delegators according to commission
	commission := tokens.MulDec(val.GetCommission())
	shared := tokens.Sub(commission)

	valBz, err := k.stakingKeeper.ValidatorAddressCodec().StringToBytes(val.GetOperator())
	if err != nil {
		return err
	}

	// update current commission
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	sdkCtx.EventManager().EmitEvent(
		sdk.NewEvent(
			types.EventTypeCommission,
			sdk.NewAttribute(sdk.AttributeKeyAmount, commission.String()),
			sdk.NewAttribute(types.AttributeKeyValidator, val.GetOperator()),
		),
	)
	currentCommission, err := k.GetValidatorAccumulatedCommission(ctx, valBz)
	if err != nil {
		return err
	}

	currentCommission.Commission = currentCommission.Commission.Add(commission...)
	err = k.SetValidatorAccumulatedCommission(ctx, valBz, currentCommission)
	if err != nil {
		return err
	}

	// update current rewards
	currentRewards, err := k.GetValidatorCurrentRewards(ctx, valBz)
	if err != nil {
		return err
	}

	currentRewards.Rewards = currentRewards.Rewards.Add(shared...)
	err = k.SetValidatorCurrentRewards(ctx, valBz, currentRewards)
	if err != nil {
		return err
	}

	// update outstanding rewards
	sdkCtx.EventManager().EmitEvent(
		sdk.NewEvent(
			types.EventTypeRewards,
			sdk.NewAttribute(sdk.AttributeKeyAmount, tokens.String()),
			sdk.NewAttribute(types.AttributeKeyValidator, val.GetOperator()),
		),
	)

	outstanding, err := k.GetValidatorOutstandingRewards(ctx, valBz)
	if err != nil {
		return err
	}

	outstanding.Rewards = outstanding.Rewards.Add(tokens...)
	return k.SetValidatorOutstandingRewards(ctx, valBz, outstanding)
}
