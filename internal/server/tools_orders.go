package server

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	t212 "github.com/netf/trading212-mcp-server/internal/trading212"
)

// OrdersResult wraps an order list so structured output is an object.
type OrdersResult struct {
	Orders []t212.Order `json:"orders"`
}

// OrderIDInput selects an order.
type OrderIDInput struct {
	OrderID int64 `json:"order_id" jsonschema:"ID of the order"`
}

type MarketOrderInput struct {
	Ticker        string  `json:"ticker" jsonschema:"ticker of the instrument to trade, e.g. AAPL_US_EQ"`
	Quantity      float64 `json:"quantity" jsonschema:"number of shares; positive buys, negative sells"`
	ExtendedHours bool    `json:"extended_hours,omitempty" jsonschema:"allow execution outside regular trading hours when the instrument supports it"`
}

type LimitOrderInput struct {
	Ticker       string            `json:"ticker" jsonschema:"ticker of the instrument to trade, e.g. AAPL_US_EQ"`
	Quantity     float64           `json:"quantity" jsonschema:"number of shares; positive buys, negative sells"`
	LimitPrice   float64           `json:"limit_price" jsonschema:"limit price for the order"`
	TimeValidity t212.TimeValidity `json:"time_validity,omitempty" jsonschema:"DAY or GOOD_TILL_CANCEL"`
}

type StopOrderInput struct {
	Ticker       string            `json:"ticker" jsonschema:"ticker of the instrument to trade, e.g. AAPL_US_EQ"`
	Quantity     float64           `json:"quantity" jsonschema:"number of shares; positive buys, negative sells"`
	StopPrice    float64           `json:"stop_price" jsonschema:"stop price that triggers the order"`
	TimeValidity t212.TimeValidity `json:"time_validity,omitempty" jsonschema:"DAY or GOOD_TILL_CANCEL"`
}

type StopLimitOrderInput struct {
	Ticker       string            `json:"ticker" jsonschema:"ticker of the instrument to trade, e.g. AAPL_US_EQ"`
	Quantity     float64           `json:"quantity" jsonschema:"number of shares; positive buys, negative sells"`
	StopPrice    float64           `json:"stop_price" jsonschema:"stop price that triggers the limit order"`
	LimitPrice   float64           `json:"limit_price" jsonschema:"limit price for the order once triggered"`
	TimeValidity t212.TimeValidity `json:"time_validity,omitempty" jsonschema:"DAY or GOOD_TILL_CANCEL"`
}

var timeValidityField = field{
	Default: t212.TimeValidityDay,
	Enum:    []any{string(t212.TimeValidityDay), string(t212.TimeValidityGoodTillCancel)},
}

// Trading 212 documents order placement as non-idempotent and limited to the
// account's primary currency.
const orderCaveats = " Orders execute in the account's primary currency. Not idempotent: never resend after an " +
	"error whose outcome is unknown; check fetch_all_orders first."

func (h *handlers) registerOrderTools(s *toolRegistry) {
	addTool(s, &mcp.Tool{
		Name:        "fetch_all_orders",
		Description: "Fetch all active (not yet filled, cancelled or expired) orders.",
		Annotations: readOnly("Pending orders"),
	}, func(ctx context.Context, _ struct{}) (OrdersResult, error) {
		o, err := h.client.Orders(ctx)
		return OrdersResult{Orders: o}, err
	})
	addTool(s, &mcp.Tool{
		Name:        "fetch_order",
		Description: "Fetch a pending order by ID.",
		Annotations: readOnly("Order by ID"),
	}, func(ctx context.Context, in OrderIDInput) (*t212.Order, error) {
		return h.client.Order(ctx, in.OrderID)
	})
	addTool(s, &mcp.Tool{
		Name:        "cancel_order",
		Description: "Request cancellation of an active order by ID. Cancellation is not guaranteed if the order is already being filled.",
		Annotations: mutating("Cancel order"),
	}, func(ctx context.Context, in OrderIDInput) (ActionResult, error) {
		if err := h.client.CancelOrder(ctx, in.OrderID); err != nil {
			return ActionResult{}, err
		}
		return ActionResult{OK: true, Message: fmt.Sprintf("cancellation requested for order %d", in.OrderID)}, nil
	})

	addTool(s, &mcp.Tool{
		Name:        "place_market_order",
		Description: "Place a market order to buy (positive quantity) or sell (negative quantity) at the next available price. If the market is closed it is queued for the next open; set extended_hours to allow filling outside the regular session." + orderCaveats,
		Annotations: mutating("Place market order"),
		InputSchema: inputSchema[MarketOrderInput](map[string]field{"extended_hours": {Default: false}}),
	}, func(ctx context.Context, in MarketOrderInput) (*t212.Order, error) {
		return h.client.PlaceMarketOrder(ctx, t212.MarketRequest{
			Ticker: in.Ticker, Quantity: in.Quantity, ExtendedHours: in.ExtendedHours,
		})
	})
	addTool(s, &mcp.Tool{
		Name:        "place_limit_order",
		Description: "Place a limit order to buy or sell at the limit price or better." + orderCaveats,
		Annotations: mutating("Place limit order"),
		InputSchema: inputSchema[LimitOrderInput](map[string]field{"time_validity": timeValidityField}),
	}, func(ctx context.Context, in LimitOrderInput) (*t212.Order, error) {
		return h.client.PlaceLimitOrder(ctx, t212.LimitRequest{
			Ticker: in.Ticker, Quantity: in.Quantity, LimitPrice: in.LimitPrice, TimeValidity: in.TimeValidity,
		})
	})
	addTool(s, &mcp.Tool{
		Name:        "place_stop_order",
		Description: "Place a stop order that becomes a market order when the last traded price reaches the stop price." + orderCaveats,
		Annotations: mutating("Place stop order"),
		InputSchema: inputSchema[StopOrderInput](map[string]field{"time_validity": timeValidityField}),
	}, func(ctx context.Context, in StopOrderInput) (*t212.Order, error) {
		return h.client.PlaceStopOrder(ctx, t212.StopRequest{
			Ticker: in.Ticker, Quantity: in.Quantity, StopPrice: in.StopPrice, TimeValidity: in.TimeValidity,
		})
	})
	addTool(s, &mcp.Tool{
		Name:        "place_stop_limit_order",
		Description: "Place a stop-limit order: when the last traded price reaches the stop price, a limit order at the limit price is placed." + orderCaveats,
		Annotations: mutating("Place stop-limit order"),
		InputSchema: inputSchema[StopLimitOrderInput](map[string]field{"time_validity": timeValidityField}),
	}, func(ctx context.Context, in StopLimitOrderInput) (*t212.Order, error) {
		return h.client.PlaceStopLimitOrder(ctx, t212.StopLimitRequest{
			Ticker: in.Ticker, Quantity: in.Quantity, StopPrice: in.StopPrice, LimitPrice: in.LimitPrice,
			TimeValidity: in.TimeValidity,
		})
	})
}
