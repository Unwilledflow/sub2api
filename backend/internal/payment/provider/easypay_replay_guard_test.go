package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/stretchr/testify/require"
)

func TestEasyPayNotificationRejectsDuplicateFields(t *testing.T) {
	for _, field := range []string{"sign", "money", "pid", "trade_no", "trade_status", "out_trade_no", "sign_type"} {
		t.Run(field, func(t *testing.T) {
			p := &EasyPay{config: map[string]string{"pid": "fixture-merchant", "pkey": "fixture-secret"}}
			params := map[string]string{"pid": "fixture-merchant", "money": "80.00", "trade_no": "fixture-trade", "out_trade_no": "fixture-order", "trade_status": "TRADE_SUCCESS", "sign_type": "MD5"}
			params["sign"] = easyPaySign(params, "fixture-secret")
			values := url.Values{}
			for k, v := range params {
				values.Set(k, v)
			}
			_, err := p.VerifyNotification(context.Background(), values.Encode(), nil)
			require.NoError(t, err)
			values.Add(field, params[field])
			_, err = p.VerifyNotification(context.Background(), values.Encode(), nil)
			require.ErrorContains(t, err, "duplicate notify parameter")
		})
	}
}

func TestEasyPayNotificationRejectsCheckoutFieldSmuggle(t *testing.T) {
	p := &EasyPay{config: map[string]string{
		"pid": "fixture-merchant", "pkey": "fixture-secret", "apiBase": "https://pay.invalid",
		"notifyUrl": "https://shop.invalid/webhook", "returnUrl": "https://shop.invalid/payment/result",
		"paymentMode": "popup",
	}}
	const fakeTradeNo = "POC-FAKE-TRADE"
	poisoned := "https://shop.invalid/payment/result?order_id=1&out_trade_no=fixture-order&status=success&trade_no=" + fakeTradeNo + "&trade_status=TRADE_SUCCESS"
	checkout, err := p.CreatePayment(context.Background(), payment.CreatePaymentRequest{
		OrderID: "fixture-order", Amount: "80.00", PaymentType: payment.TypeAlipay,
		Subject: "fixture", ReturnURL: poisoned,
	})
	require.NoError(t, err)
	parsed, err := url.Parse(checkout.PayURL)
	require.NoError(t, err)
	signed := parsed.Query()
	marker := "&trade_no=" + fakeTradeNo + "&trade_status=TRADE_SUCCESS"
	require.Contains(t, signed.Get("return_url"), marker)
	signed.Set("return_url", strings.SplitN(signed.Get("return_url"), marker, 2)[0])
	signed.Set("trade_no", fakeTradeNo)
	signed.Set("trade_status", "TRADE_SUCCESS")
	note, err := p.VerifyNotification(context.Background(), signed.Encode(), nil)
	require.ErrorContains(t, err, "unexpected notify parameter")
	require.Nil(t, note)
}

func TestEasyPayNotificationRejectsEmptyExtraParameter(t *testing.T) {
	p := &EasyPay{config: map[string]string{"pkey": "fixture-secret"}}
	params := map[string]string{
		"pid": "fixture-merchant", "money": "80.00", "trade_no": "fixture-trade",
		"out_trade_no": "fixture-order", "trade_status": "TRADE_SUCCESS", "sign_type": "MD5",
	}
	params["sign"] = easyPaySign(params, "fixture-secret")
	values := url.Values{}
	for k, v := range params {
		values.Set(k, v)
	}
	values.Set("buyer", "")
	_, err := p.VerifyNotification(context.Background(), values.Encode(), nil)
	require.ErrorContains(t, err, `unexpected notify parameter "buyer"`)
}

func TestEasyPayQueryRejectsFalseSettlementEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"error HTTP with paid fields", `{"code":1,"status":1,"money":"80.00","trade_no":"t"}`, 500},
		{"failed API with paid fields", `{"code":0,"status":1,"money":"80.00","trade_no":"t"}`, 200},
		{"missing code with paid fields", `{"status":1,"money":"80.00","trade_no":"t"}`, 200},
		{"wrong order", `{"code":1,"out_trade_no":"different-order","status":1,"money":"80.00","trade_no":"t"}`, 200},
		{"wrong nested order", `{"code":1,"data":{"out_trade_no":"different-order","status":1,"money":"80.00","trade_no":"t"}}`, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			p := newTestEasyPay(t, server.URL)
			resp, err := p.QueryOrder(context.Background(), "fixture-order")
			require.Error(t, err)
			require.Nil(t, resp)
		})
	}
}

func TestEasyPayMissingGatewayTradeNumberStaysEmpty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":1,"out_trade_no":"fixture-order","status":1,"money":"80.00"}`))
	}))
	defer server.Close()
	p := newTestEasyPay(t, server.URL)
	resp, err := p.QueryOrder(context.Background(), "fixture-order")
	require.NoError(t, err)
	require.Equal(t, payment.ProviderStatusPaid, resp.Status)
	require.Empty(t, resp.TradeNo, "merchant order number is not a gateway transaction number")
}

func TestEasyPayNotificationRejectsUnknownParams(t *testing.T) {
	// Upstream issue #7881: an order-creation signature replayed as a notify,
	// with the smuggled pair riding inside return_url. Any key outside the
	// genuine async notify set must fail closed.
	for _, extra := range []string{"return_url", "notify_url", "cid", "device", "clientip", "trade_status_extra"} {
		t.Run(extra, func(t *testing.T) {
			p := &EasyPay{config: map[string]string{"pid": "fixture-merchant", "pkey": "fixture-secret"}}
			params := map[string]string{"pid": "fixture-merchant", "money": "80.00", "trade_no": "fixture-trade", "out_trade_no": "fixture-order", "trade_status": "TRADE_SUCCESS", "sign_type": "MD5"}
			params[extra] = "x&trade_status=TRADE_SUCCESS"
			params["sign"] = easyPaySign(params, "fixture-secret")
			values := url.Values{}
			for k, v := range params {
				values.Set(k, v)
			}
			_, err := p.VerifyNotification(context.Background(), values.Encode(), nil)
			require.ErrorContains(t, err, "unexpected notify param")
		})
	}
}

func TestEasyPayNotificationAcceptsGenuineCallback(t *testing.T) {
	p := &EasyPay{config: map[string]string{"pid": "fixture-merchant", "pkey": "fixture-secret"}}
	params := map[string]string{"pid": "fixture-merchant", "money": "80.00", "trade_no": "fixture-trade", "out_trade_no": "fixture-order", "trade_status": "TRADE_SUCCESS", "sign_type": "MD5", "name": "fixture", "type": "alipay", "param": ""}
	params["sign"] = easyPaySign(params, "fixture-secret")
	values := url.Values{}
	for k, v := range params {
		values.Set(k, v)
	}
	got, err := p.VerifyNotification(context.Background(), values.Encode(), nil)
	require.NoError(t, err)
	require.NotNil(t, got)
}
