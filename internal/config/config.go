package config

import (
	"crypto/rsa"
	"encoding/base64"
	"fmt"
	"github.com/dgrijalva/jwt-go"
	"github.com/kelseyhightower/envconfig"
	"github.com/paysuper/paysuper-billing-server/pkg"
	"net/url"
	"time"
)

type PaymentSystemConfig struct {
	CardPayApiUrl        string `envconfig:"CARD_PAY_API_URL" required:"true"`
	CardPayApiSandboxUrl string `envconfig:"CARD_PAY_API_SANDBOX_URL" required:"true"`
	RedirectUrlSuccess   string `envconfig:"REDIRECT_URL_SUCCESS" default:"https://order.pay.super.com/?result=success"`
	RedirectUrlFail      string `envconfig:"REDIRECT_URL_FAIL" default:"https://order.pay.super.com/?result=fail"`
}

type CustomerTokenConfig struct {
	Length   int   `envconfig:"CUSTOMER_TOKEN_LENGTH" default:"32"`
	LifeTime int64 `envconfig:"CUSTOMER_TOKEN_LIFETIME" default:"2592000"`

	CookiePublicKeyBase64  string `envconfig:"CUSTOMER_COOKIE_PUBLIC_KEY" required:"true"`
	CookiePrivateKeyBase64 string `envconfig:"CUSTOMER_COOKIE_PRIVATE_KEY" required:"true"`
	CookiePublicKey        *rsa.PublicKey
	CookiePrivateKey       *rsa.PrivateKey
}

// CacheRedis defines the parameters for connecting to the radish server for caching objects.
type CacheRedis struct {
	Address      []string `envconfig:"CACHE_REDIS_ADDRESS" required:"true"`
	Password     string   `envconfig:"CACHE_REDIS_PASSWORD" required:"false"`
	PoolSize     int      `envconfig:"CACHE_REDIS_POOL_SIZE" default:"1"`
	MaxRetries   int      `envconfig:"CACHE_REDIS_MAX_RETRIES" default:"10"`
	MaxRedirects int      `envconfig:"CACHE_REDIS_MAX_REDIRECTS" default:"8"`
	Version      string   `envconfig:"CACHE_REDIS_VERSION" default:"1.0.5"`
}

// EmailTemplates defines of the Postmark template names for sending letters.
type EmailTemplates struct {
	ConfirmAccount                 string `envconfig:"EMAIL_CONFIRM_TEMPLATE" default:"p1_verify_letter"`
	NewRoyaltyReport               string `envconfig:"EMAIL_NEW_ROYALTY_REPORT_TEMPLATE" default:"p1_new_royalty_report"`
	NewPayout                      string `envconfig:"EMAIL_NEW_PAYOUT_TEMPLATE" default:"p1_new_payout"`
	UpdateRoyaltyReport            string `envconfig:"EMAIL_UPDATE_ROYALTY_REPORT_TEMPLATE" default:"p1_update_royalty_report"`
	VatReportChanged               string `envconfig:"EMAIL_VAT_REPORT_TEMPLATE" default:"p1_vat_report"`
	ActivationGameKey              string `envconfig:"EMAIL_ACTIVATION_CODE_TEMPLATE" default:"p1_verify_letter-1"`
	SuccessTransaction             string `envconfig:"EMAIL_SUCCESS_TRANSACTION_TEMPLATE" default:"p1_verify_letter-4"`
	RefundTransaction              string `envconfig:"EMAIL_REFUND_TRANSACTION_TEMPLATE" default:"p1_verify_letter-5"`
	OnboardingVerificationMerchant string `envconfig:"EMAIL_MERCHANT_NEW_ONBOARDING_REQUEST_TEMPLATE" default:"p1_email_merchant_new_onboarding_request_template"`
	OnboardingVerificationAdmin    string `envconfig:"EMAIL_ADMIN_NEW_ONBOARDING_REQUEST_TEMPLATE" default:"p1_email_admin_new_onboarding_request_template"`
	OnboardingCompleted            string `envconfig:"EMAIL_MERCHANT_ONBOARDING_REQUEST_COMPLETE_TEMPLATE" default:"p1_email_merchant_onboarding_request_complete_template"`
	UserInvite                     string `envconfig:"EMAIL_INVITE_TEMPLATE" default:"code-your-own"`
}

type Config struct {
	MongoDsn         string `envconfig:"MONGO_DSN" required:"true"`
	MongoDialTimeout string `envconfig:"MONGO_DIAL_TIMEOUT" required:"false" default:"10"`
	MetricsPort      string `envconfig:"METRICS_PORT" required:"false" default:"8086"`
	Environment      string `envconfig:"ENVIRONMENT" default:"dev"`
	RedisHost        string `envconfig:"REDIS_HOST" default:"127.0.0.1:6379"`
	RedisPassword    string `envconfig:"REDIS_PASSWORD" default:""`

	CentrifugoApiSecret string `envconfig:"CENTRIFUGO_API_SECRET" required:"true"`
	CentrifugoSecret    string `envconfig:"CENTRIFUGO_SECRET" required:"true"`
	CentrifugoURL       string `envconfig:"CENTRIFUGO_URL" required:"false" default:"http://127.0.0.1:8000"`
	BrokerAddress       string `envconfig:"BROKER_ADDRESS" default:"amqp://127.0.0.1:5672"`

	CentrifugoUserChannel     string `envconfig:"CENTRIFUGO_USER_CHANNEL" default:"paysuper:user#%s"`
	EmailConfirmTokenLifetime int64  `envconfig:"EMAIL_CONFIRM_TOKEN_LIFETIME" default:"86400"`

	MicroRegistry string `envconfig:"MICRO_REGISTRY" required:"false"`

	RoyaltyReportPeriod        int64  `envconfig:"ROYALTY_REPORT_PERIOD" default:"604800"`
	RoyaltyReportTimeZone      string `envconfig:"ROYALTY_REPORT_TIMEZONE" default:"Europe/Moscow"`
	RoyaltyReportAcceptTimeout int64  `envconfig:"ROYALTY_REPORT_TIMEZONE" default:"432000"`
	// moved to config for testing purposes, to prevent royalty reports tests crash on mondays before 18:00
	// must not be changed on normal app running, because it will broke royalty reports calculations
	RoyaltyReportPeriodEndHour int64 `default:"18"`

	CentrifugoMerchantChannel  string `envconfig:"CENTRIFUGO_MERCHANT_CHANNEL" default:"paysuper:merchant#%s"`
	CentrifugoFinancierChannel string `envconfig:"CENTRIFUGO_FINANCIER_CHANNEL" default:"paysuper:financier"`
	CentrifugoAdminChannel     string `envconfig:"CENTRIFUGO_ADMIN_CHANNEL" default:"paysuper:admin"`

	EmailNotificationFinancierRecipient string `envconfig:"EMAIL_NOTIFICATION_FINANCIER_RECIPIENT" required:"true"`
	EmailOnboardingAdminRecipient       string `envconfig:"EMAIL_ONBOARDING_ADMIN_RECIPIENT" required:"true"`

	OrderViewUpdateBatchSize int `envconfig:"ORDER_VIEW_UPDATE_BATCH_SIZE" default:"200"`

	HelloSignDefaultTemplate   string `envconfig:"HELLO_SIGN_DEFAULT_TEMPLATE" required:"true"`
	HelloSignAgreementClientId string `envconfig:"HELLO_SIGN_AGREEMENT_CLIENT_ID" required:"true"`

	KeyDaemonRestartInterval int64 `envconfig:"KEY_DAEMON_RESTART_INTERVAL" default:"60"`

	PaylinkMinProducts int `envconfig:"PAYLINK_MIN_PRODUCTS" required:"false" default:"1"`
	PaylinkMaxProducts int `envconfig:"PAYLINK_MAX_PRODUCTS" required:"false" default:"8"`

	CentrifugoOrderChannel string `envconfig:"CENTRIFUGO_ORDER_CHANNEL" default:"paysuper:order#%s"`

	UserInviteTokenSecret  string `envconfig:"USER_INVITE_TOKEN_SECRET" required:"true"`
	UserInviteTokenTimeout int64  `envconfig:"USER_INVITE_TOKEN_TIMEOUT" default:"48"`

	*PaymentSystemConfig
	*CustomerTokenConfig
	*CacheRedis
	*EmailTemplates

	EmailConfirmUrlParsed    *url.URL
	RedirectUrlSuccessParsed *url.URL
	RedirectUrlFailParsed    *url.URL

	MigrationsLockTimeout int64 `envconfig:"MIGRATIONS_LOCK_TIMEOUT" default:"60"`

	DashboardUrl string `envconfig:"DASHBOARD_URL" default:"https://paysupermgmt.tst.protocol.one"`
}

func NewConfig() (*Config, error) {
	cfg := &Config{}
	err := envconfig.Process("", cfg)
	if err != nil {
		return nil, err
	}

	pem, err := base64.StdEncoding.DecodeString(cfg.CookiePublicKeyBase64)

	if err != nil {
		return nil, err
	}

	cfg.CookiePublicKey, err = jwt.ParseRSAPublicKeyFromPEM(pem)

	if err != nil {
		return nil, err
	}

	pem, err = base64.StdEncoding.DecodeString(cfg.CookiePrivateKeyBase64)

	if err != nil {
		return nil, err
	}

	cfg.CookiePrivateKey, err = jwt.ParseRSAPrivateKeyFromPEM(pem)

	if err != nil {
		return nil, err
	}

	cfg.EmailConfirmUrlParsed, err = url.Parse(cfg.GetEmailConfirmUrl())

	if err != nil {
		return nil, err
	}

	cfg.RedirectUrlSuccessParsed, err = url.Parse(cfg.RedirectUrlSuccess)

	if err != nil {
		return nil, err
	}

	cfg.RedirectUrlFailParsed, err = url.Parse(cfg.RedirectUrlFail)

	if err != nil {
		return nil, err
	}

	return cfg, err
}

func (cfg *Config) GetCustomerTokenLength() int {
	return cfg.CustomerTokenConfig.Length
}

func (cfg *Config) GetCustomerTokenExpire() time.Duration {
	return time.Second * time.Duration(cfg.CustomerTokenConfig.LifeTime)
}

func (cfg *Config) GetEmailConfirmTokenLifetime() time.Duration {
	return time.Second * time.Duration(cfg.EmailConfirmTokenLifetime)
}

func (cfg *Config) GetUserConfirmEmailUrl(params map[string]string) string {
	query := cfg.EmailConfirmUrlParsed.Query()

	for k, v := range params {
		query.Set(k, v)
	}

	cfg.EmailConfirmUrlParsed.RawQuery = query.Encode()

	return cfg.EmailConfirmUrlParsed.String()
}

func (cfg *Config) GetRedirectUrlSuccess(params map[string]string) string {
	query := cfg.RedirectUrlSuccessParsed.Query()

	for k, v := range params {
		query.Set(k, v)
	}

	cfg.RedirectUrlSuccessParsed.RawQuery = query.Encode()
	return cfg.RedirectUrlSuccessParsed.String()
}

func (cfg *Config) GetRedirectUrlFail(params map[string]string) string {
	query := cfg.RedirectUrlFailParsed.Query()

	for k, v := range params {
		query.Set(k, v)
	}

	cfg.RedirectUrlFailParsed.RawQuery = query.Encode()
	return cfg.RedirectUrlFailParsed.String()
}

func (cfg *Config) GetCentrifugoOrderChannel(orderUuid string) string {
	return fmt.Sprintf(cfg.CentrifugoOrderChannel, orderUuid)
}

func (cfg *Config) GetReceiptPurchaseUrl(transactionId, receiptId string) string {
	return fmt.Sprintf(pkg.ReceiptPurchaseUrl, cfg.DashboardUrl, receiptId, transactionId)
}

func (cfg *Config) GetReceiptRefundUrl(transactionId, receiptId string) string {
	return fmt.Sprintf(pkg.ReceiptRefundUrl, cfg.DashboardUrl, receiptId, transactionId)
}

func (cfg *Config) GetEmailConfirmUrl() string {
	return fmt.Sprintf(pkg.EmailConfirmUrl, cfg.DashboardUrl)
}

func (cfg *Config) GetRoyaltyReportsUrl() string {
	return fmt.Sprintf(pkg.RoyaltyReportsUrl, cfg.DashboardUrl)
}

func (cfg *Config) GetPayoutsUrl() string {
	return fmt.Sprintf(pkg.PayoutsUrl, cfg.DashboardUrl)
}

func (cfg *Config) GetMerchantCompanyUrl() string {
	return fmt.Sprintf(pkg.MerchantCompanyUrl, cfg.DashboardUrl)
}

func (cfg *Config) GetAdminCompanyUrl(merchantId string) string {
	return fmt.Sprintf(pkg.AdminCompanyUrl, cfg.DashboardUrl, merchantId)
}

func (cfg *Config) GetAdminOnboardingRequestsUrl() string {
	return fmt.Sprintf(pkg.AdminOnboardingRequestsUrl, cfg.DashboardUrl)
}

func (cfg *Config) GetUserInviteUrl(token string) string {
	return fmt.Sprintf(pkg.UserInviteUrl, cfg.DashboardUrl, token)
}
