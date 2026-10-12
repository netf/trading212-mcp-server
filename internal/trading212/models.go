package trading212

import "time"

// Response fields are pointers because the Trading 212 API documents almost
// all of them as optional; unknown fields are ignored on decode so additive
// upstream changes do not break the client.

// Cash is the cash breakdown inside an account summary.
type Cash struct {
	AvailableToTrade  *float64 `json:"availableToTrade,omitempty"`
	InPies            *float64 `json:"inPies,omitempty"`
	ReservedForOrders *float64 `json:"reservedForOrders,omitempty"`
}

// Investments summarises invested value and profit/loss.
type Investments struct {
	CurrentValue         *float64 `json:"currentValue,omitempty"`
	RealizedProfitLoss   *float64 `json:"realizedProfitLoss,omitempty"`
	TotalCost            *float64 `json:"totalCost,omitempty"`
	UnrealizedProfitLoss *float64 `json:"unrealizedProfitLoss,omitempty"`
}

// AccountSummary is returned by GET /equity/account/summary.
type AccountSummary struct {
	Cash        *Cash        `json:"cash,omitempty"`
	Currency    *string      `json:"currency,omitempty"`
	ID          *int64       `json:"id,omitempty"`
	Investments *Investments `json:"investments,omitempty"`
	TotalValue  *float64     `json:"totalValue,omitempty"`
}

// Instrument is the short instrument description embedded in other objects.
type Instrument struct {
	Currency *string `json:"currency,omitempty"`
	ISIN     *string `json:"isin,omitempty"`
	Name     *string `json:"name,omitempty"`
	Ticker   *string `json:"ticker,omitempty"`
}

// PositionWalletImpact describes a position's value in the account currency.
type PositionWalletImpact struct {
	Currency             *string  `json:"currency,omitempty"`
	CurrentValue         *float64 `json:"currentValue,omitempty"`
	FXImpact             *float64 `json:"fxImpact,omitempty"`
	TotalCost            *float64 `json:"totalCost,omitempty"`
	UnrealizedProfitLoss *float64 `json:"unrealizedProfitLoss,omitempty"`
}

// Position is an open position.
type Position struct {
	AveragePricePaid            *float64              `json:"averagePricePaid,omitempty"`
	CreatedAt                   *time.Time            `json:"createdAt,omitempty"`
	CurrentPrice                *float64              `json:"currentPrice,omitempty"`
	Instrument                  *Instrument           `json:"instrument,omitempty"`
	Quantity                    *float64              `json:"quantity,omitempty"`
	QuantityAvailableForTrading *float64              `json:"quantityAvailableForTrading,omitempty"`
	QuantityInPies              *float64              `json:"quantityInPies,omitempty"`
	WalletImpact                *PositionWalletImpact `json:"walletImpact,omitempty"`
}

// TimeValidity controls how long a pending order stays active.
type TimeValidity string

const (
	TimeValidityDay            TimeValidity = "DAY"
	TimeValidityGoodTillCancel TimeValidity = "GOOD_TILL_CANCEL"
)

// Valid reports whether v is a known time validity.
func (v TimeValidity) Valid() bool {
	switch v {
	case TimeValidityDay, TimeValidityGoodTillCancel:
		return true
	default:
		return false
	}
}

type OrderStatus string

const (
	OrderStatusLocal           OrderStatus = "LOCAL"
	OrderStatusUnconfirmed     OrderStatus = "UNCONFIRMED"
	OrderStatusConfirmed       OrderStatus = "CONFIRMED"
	OrderStatusNew             OrderStatus = "NEW"
	OrderStatusCancelling      OrderStatus = "CANCELLING"
	OrderStatusCancelled       OrderStatus = "CANCELLED"
	OrderStatusPartiallyFilled OrderStatus = "PARTIALLY_FILLED"
	OrderStatusFilled          OrderStatus = "FILLED"
	OrderStatusRejected        OrderStatus = "REJECTED"
	OrderStatusReplacing       OrderStatus = "REPLACING"
	OrderStatusReplaced        OrderStatus = "REPLACED"
)

type OrderStrategy string

const (
	OrderStrategyQuantity OrderStrategy = "QUANTITY"
	OrderStrategyValue    OrderStrategy = "VALUE"
)

type OrderType string

const (
	OrderTypeLimit     OrderType = "LIMIT"
	OrderTypeStop      OrderType = "STOP"
	OrderTypeMarket    OrderType = "MARKET"
	OrderTypeStopLimit OrderType = "STOP_LIMIT"
)

type OrderSide string

const (
	OrderSideBuy  OrderSide = "BUY"
	OrderSideSell OrderSide = "SELL"
)

type InitiatedFrom string

const (
	InitiatedFromAPI                  InitiatedFrom = "API"
	InitiatedFromIOS                  InitiatedFrom = "IOS"
	InitiatedFromAndroid              InitiatedFrom = "ANDROID"
	InitiatedFromWeb                  InitiatedFrom = "WEB"
	InitiatedFromSystem               InitiatedFrom = "SYSTEM"
	InitiatedFromAutoinvest           InitiatedFrom = "AUTOINVEST"
	InitiatedFromInstrumentAutoinvest InitiatedFrom = "INSTRUMENT_AUTOINVEST"
)

// Order is a pending or historical equity order.
type Order struct {
	CreatedAt      *time.Time     `json:"createdAt,omitempty"`
	Currency       *string        `json:"currency,omitempty"`
	ExtendedHours  *bool          `json:"extendedHours,omitempty"`
	FilledQuantity *float64       `json:"filledQuantity,omitempty"`
	FilledValue    *float64       `json:"filledValue,omitempty"`
	ID             *int64         `json:"id,omitempty"`
	InitiatedFrom  *InitiatedFrom `json:"initiatedFrom,omitempty"`
	Instrument     *Instrument    `json:"instrument,omitempty"`
	LimitPrice     *float64       `json:"limitPrice,omitempty"`
	Quantity       *float64       `json:"quantity,omitempty"`
	Side           *OrderSide     `json:"side,omitempty"`
	Status         *OrderStatus   `json:"status,omitempty"`
	StopPrice      *float64       `json:"stopPrice,omitempty"`
	Strategy       *OrderStrategy `json:"strategy,omitempty"`
	Ticker         *string        `json:"ticker,omitempty"`
	TimeInForce    *TimeValidity  `json:"timeInForce,omitempty"`
	Type           *OrderType     `json:"type,omitempty"`
	Value          *float64       `json:"value,omitempty"`
}

// MarketRequest is the body of POST /equity/orders/market.
type MarketRequest struct {
	ExtendedHours bool    `json:"extendedHours"`
	Quantity      float64 `json:"quantity"`
	Ticker        string  `json:"ticker"`
}

// LimitRequest is the body of POST /equity/orders/limit.
type LimitRequest struct {
	LimitPrice   float64      `json:"limitPrice"`
	Quantity     float64      `json:"quantity"`
	Ticker       string       `json:"ticker"`
	TimeValidity TimeValidity `json:"timeValidity"`
}

// StopRequest is the body of POST /equity/orders/stop.
type StopRequest struct {
	Quantity     float64      `json:"quantity"`
	StopPrice    float64      `json:"stopPrice"`
	Ticker       string       `json:"ticker"`
	TimeValidity TimeValidity `json:"timeValidity"`
}

// StopLimitRequest is the body of POST /equity/orders/stop_limit.
type StopLimitRequest struct {
	LimitPrice   float64      `json:"limitPrice"`
	Quantity     float64      `json:"quantity"`
	StopPrice    float64      `json:"stopPrice"`
	Ticker       string       `json:"ticker"`
	TimeValidity TimeValidity `json:"timeValidity"`
}

type TimeEventType string

const (
	TimeEventOpen            TimeEventType = "OPEN"
	TimeEventClose           TimeEventType = "CLOSE"
	TimeEventBreakStart      TimeEventType = "BREAK_START"
	TimeEventBreakEnd        TimeEventType = "BREAK_END"
	TimeEventPreMarketOpen   TimeEventType = "PRE_MARKET_OPEN"
	TimeEventAfterHoursOpen  TimeEventType = "AFTER_HOURS_OPEN"
	TimeEventAfterHoursClose TimeEventType = "AFTER_HOURS_CLOSE"
	TimeEventOvernightOpen   TimeEventType = "OVERNIGHT_OPEN"
)

type TimeEvent struct {
	Date time.Time     `json:"date"`
	Type TimeEventType `json:"type"`
}

type WorkingSchedule struct {
	ID         int64       `json:"id"`
	TimeEvents []TimeEvent `json:"timeEvents"`
}

// Exchange is returned by GET /equity/metadata/exchanges.
type Exchange struct {
	ID               int64             `json:"id"`
	Name             string            `json:"name"`
	WorkingSchedules []WorkingSchedule `json:"workingSchedules"`
}

type InstrumentType string

const (
	InstrumentTypeCryptocurrency InstrumentType = "CRYPTOCURRENCY"
	InstrumentTypeETF            InstrumentType = "ETF"
	InstrumentTypeForex          InstrumentType = "FOREX"
	InstrumentTypeFutures        InstrumentType = "FUTURES"
	InstrumentTypeIndex          InstrumentType = "INDEX"
	InstrumentTypeStock          InstrumentType = "STOCK"
	InstrumentTypeWarrant        InstrumentType = "WARRANT"
	InstrumentTypeCrypto         InstrumentType = "CRYPTO"
	InstrumentTypeCVR            InstrumentType = "CVR"
	InstrumentTypeCorpAct        InstrumentType = "CORPACT"
)

// TradeableInstrument is returned by GET /equity/metadata/instruments.
type TradeableInstrument struct {
	AddedOn           *time.Time      `json:"addedOn,omitempty"`
	CurrencyCode      *string         `json:"currencyCode,omitempty"`
	ExtendedHours     *bool           `json:"extendedHours,omitempty"`
	ISIN              *string         `json:"isin,omitempty"`
	MaxOpenQuantity   *float64        `json:"maxOpenQuantity,omitempty"`
	Name              *string         `json:"name,omitempty"`
	ShortName         *string         `json:"shortName,omitempty"`
	Ticker            *string         `json:"ticker,omitempty"`
	Type              *InstrumentType `json:"type,omitempty"`
	WorkingScheduleID *int64          `json:"workingScheduleId,omitempty"`
}

type DividendCashAction string

const (
	DividendCashActionReinvest      DividendCashAction = "REINVEST"
	DividendCashActionToAccountCash DividendCashAction = "TO_ACCOUNT_CASH"
)

// Valid reports whether a is a known dividend cash action.
func (a DividendCashAction) Valid() bool {
	switch a {
	case DividendCashActionReinvest, DividendCashActionToAccountCash:
		return true
	default:
		return false
	}
}

type PieStatus string

const (
	PieStatusAhead   PieStatus = "AHEAD"
	PieStatusOnTrack PieStatus = "ON_TRACK"
	PieStatusBehind  PieStatus = "BEHIND"
)

type InvestmentResult struct {
	PriceAvgInvestedValue *float64 `json:"priceAvgInvestedValue,omitempty"`
	PriceAvgResult        *float64 `json:"priceAvgResult,omitempty"`
	PriceAvgResultCoef    *float64 `json:"priceAvgResultCoef,omitempty"`
	PriceAvgValue         *float64 `json:"priceAvgValue,omitempty"`
}

type DividendDetails struct {
	Gained     *float64 `json:"gained,omitempty"`
	InCash     *float64 `json:"inCash,omitempty"`
	Reinvested *float64 `json:"reinvested,omitempty"`
}

// Pie is an entry of GET /equity/pies.
type Pie struct {
	Cash            *float64          `json:"cash,omitempty"`
	DividendDetails *DividendDetails  `json:"dividendDetails,omitempty"`
	ID              *int64            `json:"id,omitempty"`
	Progress        *float64          `json:"progress,omitempty"`
	Result          *InvestmentResult `json:"result,omitempty"`
	Status          *PieStatus        `json:"status,omitempty"`
}

// InstrumentIssueName is, for example, DELISTED, SUSPENDED or
// MAX_POSITION_SIZE_REACHED.
type InstrumentIssueName string

type InstrumentIssueSeverity string

const (
	IssueSeverityIrreversible InstrumentIssueSeverity = "IRREVERSIBLE"
	IssueSeverityReversible   InstrumentIssueSeverity = "REVERSIBLE"
	IssueSeverityInformative  InstrumentIssueSeverity = "INFORMATIVE"
)

type InstrumentIssue struct {
	Name     *InstrumentIssueName     `json:"name,omitempty"`
	Severity *InstrumentIssueSeverity `json:"severity,omitempty"`
}

type PieInstrument struct {
	CurrentShare  *float64          `json:"currentShare,omitempty"`
	ExpectedShare *float64          `json:"expectedShare,omitempty"`
	Issues        []InstrumentIssue `json:"issues,omitempty"`
	OwnedQuantity *float64          `json:"ownedQuantity,omitempty"`
	Result        *InvestmentResult `json:"result,omitempty"`
	Ticker        *string           `json:"ticker,omitempty"`
}

type PieSettings struct {
	CreationDate       *time.Time          `json:"creationDate,omitempty"`
	DividendCashAction *DividendCashAction `json:"dividendCashAction,omitempty"`
	EndDate            *time.Time          `json:"endDate,omitempty"`
	Goal               *float64            `json:"goal,omitempty"`
	Icon               *string             `json:"icon,omitempty"`
	ID                 *int64              `json:"id,omitempty"`
	InitialInvestment  *float64            `json:"initialInvestment,omitempty"`
	InstrumentShares   map[string]float64  `json:"instrumentShares,omitempty"`
	Name               *string             `json:"name,omitempty"`
	PublicURL          *string             `json:"publicUrl,omitempty"`
}

// PieDetails is returned when fetching, creating, updating or duplicating a pie.
type PieDetails struct {
	Instruments []PieInstrument `json:"instruments,omitempty"`
	Settings    *PieSettings    `json:"settings,omitempty"`
}

// PieRequest is the body for creating or updating a pie.
type PieRequest struct {
	DividendCashAction *DividendCashAction `json:"dividendCashAction,omitempty"`
	EndDate            *time.Time          `json:"endDate,omitempty"`
	Goal               *float64            `json:"goal,omitempty"`
	Icon               *string             `json:"icon,omitempty"`
	InstrumentShares   map[string]float64  `json:"instrumentShares,omitempty"`
	Name               *string             `json:"name,omitempty"`
}

// DuplicatePieRequest is the body of POST /equity/pies/{id}/duplicate.
type DuplicatePieRequest struct {
	Icon *string `json:"icon,omitempty"`
	Name *string `json:"name,omitempty"`
}

// TaxName is, for example, STAMP_DUTY or CURRENCY_CONVERSION_FEE.
type TaxName string

type Tax struct {
	ChargedAt *time.Time `json:"chargedAt,omitempty"`
	Currency  *string    `json:"currency,omitempty"`
	Name      *TaxName   `json:"name,omitempty"`
	Quantity  *float64   `json:"quantity,omitempty"`
}

type FillWalletImpact struct {
	Currency           *string  `json:"currency,omitempty"`
	FXRate             *float64 `json:"fxRate,omitempty"`
	NetValue           *float64 `json:"netValue,omitempty"`
	RealisedProfitLoss *float64 `json:"realisedProfitLoss,omitempty"`
	Taxes              []Tax    `json:"taxes,omitempty"`
}

type TradingMethod string

const (
	TradingMethodTOTV TradingMethod = "TOTV"
	TradingMethodOTC  TradingMethod = "OTC"
)

// FillType is TRADE for ordinary executions, or a corporate action such as
// STOCK_SPLIT or SPIN_OFF.
type FillType string

type Fill struct {
	FilledAt      *time.Time        `json:"filledAt,omitempty"`
	ID            *int64            `json:"id,omitempty"`
	Price         *float64          `json:"price,omitempty"`
	Quantity      *float64          `json:"quantity,omitempty"`
	TradingMethod *TradingMethod    `json:"tradingMethod,omitempty"`
	Type          *FillType         `json:"type,omitempty"`
	WalletImpact  *FillWalletImpact `json:"walletImpact,omitempty"`
}

type HistoricalOrder struct {
	Fill  *Fill  `json:"fill,omitempty"`
	Order *Order `json:"order,omitempty"`
}

// DividendType is, for example, ORDINARY, BONUS or RETURN_OF_CAPITAL.
type DividendType string

type DividendItem struct {
	Amount              *float64      `json:"amount,omitempty"`
	AmountInEuro        *float64      `json:"amountInEuro,omitempty"`
	Currency            *string       `json:"currency,omitempty"`
	GrossAmountPerShare *float64      `json:"grossAmountPerShare,omitempty"`
	Instrument          *Instrument   `json:"instrument,omitempty"`
	PaidOn              *time.Time    `json:"paidOn,omitempty"`
	Quantity            *float64      `json:"quantity,omitempty"`
	Reference           *string       `json:"reference,omitempty"`
	Ticker              *string       `json:"ticker,omitempty"`
	TickerCurrency      *string       `json:"tickerCurrency,omitempty"`
	Type                *DividendType `json:"type,omitempty"`
}

type TransactionType string

const (
	TransactionWithdraw           TransactionType = "WITHDRAW"
	TransactionDeposit            TransactionType = "DEPOSIT"
	TransactionFee                TransactionType = "FEE"
	TransactionTransfer           TransactionType = "TRANSFER"
	TransactionInterestOnFreeCash TransactionType = "INTEREST_ON_FREE_CASH"
	TransactionLendingInterest    TransactionType = "LENDING_INTEREST"
)

type TransactionItem struct {
	Amount    *float64         `json:"amount,omitempty"`
	Currency  *string          `json:"currency,omitempty"`
	DateTime  *time.Time       `json:"dateTime,omitempty"`
	Reference *string          `json:"reference,omitempty"`
	Type      *TransactionType `json:"type,omitempty"`
}

// Page is a cursor-paginated history response. NextPagePath, when present,
// holds the query string for the next page.
type Page[T any] struct {
	Items        []T     `json:"items"`
	NextPagePath *string `json:"nextPagePath,omitempty"`
}

type ReportStatus string

const (
	ReportStatusQueued     ReportStatus = "Queued"
	ReportStatusProcessing ReportStatus = "Processing"
	ReportStatusRunning    ReportStatus = "Running"
	ReportStatusCanceled   ReportStatus = "Canceled"
	ReportStatusFailed     ReportStatus = "Failed"
	ReportStatusFinished   ReportStatus = "Finished"
)

// ReportDataIncluded selects which data sets a CSV export contains.
type ReportDataIncluded struct {
	IncludeDividends    bool `json:"includeDividends"`
	IncludeInterest     bool `json:"includeInterest"`
	IncludeOrders       bool `json:"includeOrders"`
	IncludeTransactions bool `json:"includeTransactions"`
}

// ExportRequest is the body of POST /equity/history/exports.
type ExportRequest struct {
	DataIncluded ReportDataIncluded `json:"dataIncluded"`
	TimeFrom     *time.Time         `json:"timeFrom,omitempty"`
	TimeTo       *time.Time         `json:"timeTo,omitempty"`
}

// EnqueuedReport is returned after requesting an export.
type EnqueuedReport struct {
	ReportID int64 `json:"reportId"`
}

// Report is an entry of GET /equity/history/exports.
type Report struct {
	DataIncluded *ReportDataIncluded `json:"dataIncluded,omitempty"`
	DownloadLink *string             `json:"downloadLink,omitempty"`
	ReportID     *int64              `json:"reportId,omitempty"`
	Status       *ReportStatus       `json:"status,omitempty"`
	TimeFrom     *time.Time          `json:"timeFrom,omitempty"`
	TimeTo       *time.Time          `json:"timeTo,omitempty"`
}
