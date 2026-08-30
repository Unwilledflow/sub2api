package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/payment"
)

func TestEasyPayQueryOrderStatusMapping(t *testing.T) {
	t.Parallel()

	const orderID = "order-123"
	tests := []struct {
		name        string
		body        string
		wantStatus  string
		wantTradeNo string
		wantAmount  float64
	}{
		{
			name:        "top level trade success is paid",
			body:        `{"code":200,"trade_status":"TRADE_SUCCESS","status":0,"money":"12.34","trade_no":"gateway-123"}`,
			wantStatus:  payment.ProviderStatusPaid,
			wantTradeNo: "gateway-123",
			wantAmount:  12.34,
		},
		{
			name:        "waiting trade status with paid numeric status stays pending",
			body:        `{"code":1,"trade_status":"WAITING","status":1,"money":"12.34","trade_no":"gateway-123"}`,
			wantStatus:  payment.ProviderStatusPending,
			wantTradeNo: "gateway-123",
			wantAmount:  12.34,
		},
		{
			name:        "empty trade status with paid numeric status stays pending",
			body:        `{"code":1,"trade_status":"","status":1,"money":"12.34"}`,
			wantStatus:  payment.ProviderStatusPending,
			wantTradeNo: orderID,
			wantAmount:  12.34,
		},
		{
			name:        "nested data trade success is paid",
			body:        `{"code":1,"data":{"trade_status":"TRADE_SUCCESS","status":0,"money":"9.99","trade_no":"data-456"}}`,
			wantStatus:  payment.ProviderStatusPaid,
			wantTradeNo: "data-456",
			wantAmount:  9.99,
		},
		{
			name:        "legacy numeric paid status remains compatible",
			body:        `{"code":1,"status":1,"money":"3.21"}`,
			wantStatus:  payment.ProviderStatusPaid,
			wantTradeNo: orderID,
			wantAmount:  3.21,
		},
		{
			name:        "legacy numeric non paid status is pending",
			body:        `{"code":1,"status":0,"money":"3.21"}`,
			wantStatus:  payment.ProviderStatusPending,
			wantTradeNo: orderID,
			wantAmount:  3.21,
		},
		{
			name:        "query failure with missing status is pending",
			body:        `{"code":0,"msg":"订单不存在"}`,
			wantStatus:  payment.ProviderStatusPending,
			wantTradeNo: orderID,
		},
		{
			name:        "missing fields are pending",
			body:        `{}`,
			wantStatus:  payment.ProviderStatusPending,
			wantTradeNo: orderID,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var gotForm url.Values
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Errorf("method = %q, want %q", r.Method, http.MethodGet)
				}
				if r.URL.Path != "/api.php" {
					t.Errorf("path = %q, want /api.php", r.URL.Path)
				}
				if err := r.ParseForm(); err != nil {
					t.Errorf("ParseForm: %v", err)
				}
				gotForm = make(url.Values, len(r.URL.Query()))
				for key, values := range r.URL.Query() {
					gotForm[key] = append([]string(nil), values...)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			provider := newTestEasyPay(t, server.URL)
			resp, err := provider.QueryOrder(context.Background(), orderID)
			if err != nil {
				t.Fatalf("QueryOrder returned error: %v", err)
			}
			if resp.Status != tt.wantStatus {
				t.Fatalf("status = %q, want %q (response=%+v)", resp.Status, tt.wantStatus, resp)
			}
			if resp.TradeNo != tt.wantTradeNo {
				t.Fatalf("trade_no = %q, want %q", resp.TradeNo, tt.wantTradeNo)
			}
			if resp.Amount != tt.wantAmount {
				t.Fatalf("amount = %v, want %v", resp.Amount, tt.wantAmount)
			}
			for key, want := range map[string]string{
				"act":          "order",
				"pid":          "pid-1",
				"key":          "pkey-1",
				"out_trade_no": orderID,
			} {
				if got := gotForm.Get(key); got != want {
					t.Fatalf("form[%s] = %q, want %q (form=%v)", key, got, want, gotForm)
				}
			}
		})
	}
}

func TestEasyPayCreateAPIPaymentSuccessCodes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		code    int
		wantErr bool
	}{
		{name: "legacy success", code: 1},
		{name: "HTTP style success", code: 200},
		{name: "provider failure", code: 201, wantErr: true},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					t.Errorf("method = %q, want %q", r.Method, http.MethodPost)
				}
				if r.URL.Path != "/mapi.php" {
					t.Errorf("path = %q, want /mapi.php", r.URL.Path)
				}
				if err := r.ParseForm(); err != nil {
					t.Fatalf("ParseForm: %v", err)
				}
				if got := r.PostForm.Get("pid"); got != "pid-1" {
					t.Errorf("pid = %q, want pid-1", got)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprintf(w, `{"code":%d,"msg":"result","trade_no":"gateway-order","payurl":"https://pay.example.com/order","qrcode":"qr-value"}`, tt.code)
			}))
			defer server.Close()

			provider := newTestEasyPay(t, server.URL)
			resp, err := provider.CreatePayment(context.Background(), payment.CreatePaymentRequest{
				OrderID:     "order-create",
				Amount:      "1.00",
				Subject:     "test order",
				PaymentType: payment.TypeAlipay,
				ClientIP:    "127.0.0.1",
			})
			if tt.wantErr {
				if err == nil {
					t.Fatalf("CreatePayment returned no error (response=%+v)", resp)
				}
				return
			}
			if err != nil {
				t.Fatalf("CreatePayment: %v", err)
			}
			if resp.TradeNo != "gateway-order" || resp.PayURL != "https://pay.example.com/order" || resp.QRCode != "qr-value" {
				t.Fatalf("response = %+v", resp)
			}
		})
	}
}

func TestEasyPayQueryOrderFallsBackToDocumentedFindOrderEndpoint(t *testing.T) {
	t.Parallel()

	const orderID = "merchant-order-123"
	var standardCalls, fallbackCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api.php":
			standardCalls++
			if r.Method != http.MethodGet {
				t.Errorf("standard method = %q, want GET", r.Method)
			}
			http.NotFound(w, r)
		case "/api/findorder":
			fallbackCalls++
			if r.Method != http.MethodPost {
				t.Errorf("fallback method = %q, want POST", r.Method)
			}
			if err := r.ParseForm(); err != nil {
				t.Fatalf("ParseForm: %v", err)
			}
			if got := r.PostForm.Get("order_no"); got != orderID {
				t.Errorf("order_no = %q, want %q", got, orderID)
			}
			if got := r.PostForm.Get("type"); got != "2" {
				t.Errorf("type = %q, want 2", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"code":200,"msg":"ok","data":{"status":1,"money":"8.88","trade_no":"gateway-456"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	provider := newTestEasyPay(t, server.URL)
	resp, err := provider.QueryOrder(context.Background(), orderID)
	if err != nil {
		t.Fatalf("QueryOrder: %v", err)
	}
	if standardCalls != 1 || fallbackCalls != 1 {
		t.Fatalf("calls standard=%d fallback=%d, want 1 each", standardCalls, fallbackCalls)
	}
	if resp.Status != payment.ProviderStatusPaid || resp.TradeNo != "gateway-456" || resp.Amount != 8.88 {
		t.Fatalf("response = %+v", resp)
	}
}

func TestEasyPayQueryOrderRedactsCredentialsFromTransportErrors(t *testing.T) {
	t.Parallel()

	provider := newTestEasyPay(t, "https://pay.example.com")
	var requestURL string
	provider.httpClient = &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		requestURL = req.URL.String()
		return nil, errors.New("dial failed")
	})}

	_, err := provider.QueryOrder(context.Background(), "order-secret-test")
	if err == nil {
		t.Fatal("QueryOrder returned no error")
	}
	if !strings.Contains(requestURL, "key=pkey-1") {
		t.Fatalf("request URL did not contain protocol key parameter: %q", requestURL)
	}
	if strings.Contains(err.Error(), "pkey-1") || strings.Contains(err.Error(), "order-secret-test") {
		t.Fatalf("error leaked query credentials: %v", err)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
