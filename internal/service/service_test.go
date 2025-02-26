package service

import (
	"context"
	"github.com/golang/protobuf/ptypes"
	casbinMocks "github.com/paysuper/casbin-server/pkg/mocks"
	"github.com/paysuper/paysuper-billing-server/internal/config"
	"github.com/paysuper/paysuper-billing-server/internal/mocks"
	"github.com/paysuper/paysuper-billing-server/pkg"
	"github.com/paysuper/paysuper-billing-server/pkg/proto/billing"
	"github.com/paysuper/paysuper-billing-server/pkg/proto/grpc"
	reportingMocks "github.com/paysuper/paysuper-reporter/pkg/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.uber.org/zap"
	"gopkg.in/ProtocolONE/rabbitmq.v1/pkg"
	mongodb "gopkg.in/paysuper/paysuper-database-mongo.v1"
	"testing"
	"time"
)

type BillingServiceTestSuite struct {
	suite.Suite
	db      *mongodb.Source
	log     *zap.Logger
	cfg     *config.Config
	exCh    chan bool
	service *Service
	cache   CacheInterface

	project *billing.Project
}

func Test_BillingService(t *testing.T) {
	suite.Run(t, new(BillingServiceTestSuite))
}

func (suite *BillingServiceTestSuite) SetupTest() {
	cfg, err := config.NewConfig()
	if err != nil {
		suite.FailNow("Config load failed", "%v", err)
	}

	suite.cfg = cfg

	db, err := mongodb.NewDatabase()
	if err != nil {
		suite.FailNow("Database connection failed", "%v", err)
	}

	suite.db = db

	suite.log, err = zap.NewProduction()
	if err != nil {
		suite.FailNow("Logger initialization failed", "%v", err)
	}

	broker, err := rabbitmq.NewBroker(cfg.BrokerAddress)
	if err != nil {
		suite.FailNow("Creating RabbitMQ publisher failed", "%v", err)
	}

	redisdb := mocks.NewTestRedis()
	cache, err := NewCacheRedis(redisdb, "cache")
	suite.service = NewBillingService(
		db,
		cfg,
		mocks.NewGeoIpServiceTestOk(),
		mocks.NewRepositoryServiceOk(),
		mocks.NewTaxServiceOkMock(),
		broker,
		nil,
		cache,
		mocks.NewCurrencyServiceMockOk(),
		mocks.NewDocumentSignerMockOk(),
		&reportingMocks.ReporterService{},
		mocks.NewFormatterOK(),
		mocks.NewBrokerMockOk(),
		&casbinMocks.CasbinService{},
	)

	if err := suite.service.Init(); err != nil {
		suite.FailNow("Billing service initialization failed", "%v", err)
	}

	ps := &billing.PaymentSystem{
		Id:                 primitive.NewObjectID().Hex(),
		Name:               "CardPay",
		AccountingCurrency: "RUB",
		AccountingPeriod:   "every-day",
		Country:            "",
		IsActive:           true,
	}

	country := &billing.Country{
		IsoCodeA2:       "RU",
		Region:          "Russia",
		Currency:        "RUB",
		PaymentsAllowed: true,
		ChangeAllowed:   true,
		VatEnabled:      true,
		PriceGroupId:    "",
		VatCurrency:     "RUB",
	}

	pmBankCard := &billing.PaymentMethod{
		Id:               primitive.NewObjectID().Hex(),
		Name:             "Bank card",
		Group:            "BANKCARD",
		MinPaymentAmount: 100,
		MaxPaymentAmount: 15000,
		ExternalId:       "BANKCARD",
		TestSettings: map[string]*billing.PaymentMethodParams{
			"RUB": {
				Currency:       "RUB",
				TerminalId:     "15985",
				Secret:         "A1tph4I6BD0f",
				SecretCallback: "0V1rJ7t4jCRv",
			},
		},
		Type:            "bank_card",
		IsActive:        true,
		PaymentSystemId: ps.Id,
	}

	date, err := ptypes.TimestampProto(time.Now().Add(time.Hour * -360))
	assert.NoError(suite.T(), err, "Generate merchant date failed")

	merchant := &billing.Merchant{
		Id: primitive.NewObjectID().Hex(),
		Company: &billing.MerchantCompanyInfo{
			Name:    "merchant1",
			Country: "RU",
			Zip:     "190000",
			City:    "St.Petersburg",
		},
		Contacts: &billing.MerchantContact{
			Authorized: &billing.MerchantContactAuthorized{
				Name:     "Unit Test",
				Email:    "test@unit.test",
				Phone:    "123456789",
				Position: "Unit Test",
			},
			Technical: &billing.MerchantContactTechnical{
				Name:  "Unit Test",
				Email: "test@unit.test",
				Phone: "123456789",
			},
		},
		Banking: &billing.MerchantBanking{
			Currency: "RUB",
			Name:     "Bank name",
		},
		IsVatEnabled:              true,
		IsCommissionToUserEnabled: true,
		Status:                    pkg.MerchantStatusDraft,
		LastPayout: &billing.MerchantLastPayout{
			Date:   date,
			Amount: 999999,
		},
		IsSigned: true,
		PaymentMethods: map[string]*billing.MerchantPaymentMethod{
			pmBankCard.Id: {
				PaymentMethod: &billing.MerchantPaymentMethodIdentification{
					Id:   pmBankCard.Id,
					Name: pmBankCard.Name,
				},
				Commission: &billing.MerchantPaymentMethodCommissions{
					Fee: 2.5,
					PerTransaction: &billing.MerchantPaymentMethodPerTransactionCommission{
						Fee:      30,
						Currency: "RUB",
					},
				},
				Integration: &billing.MerchantPaymentMethodIntegration{
					TerminalId:       "1234567890",
					TerminalPassword: "0987654321",
					Integrated:       true,
				},
				IsActive: true,
			},
		},
	}

	projectDefault := &billing.Project{
		Id:                       primitive.NewObjectID().Hex(),
		CallbackCurrency:         "RUB",
		CallbackProtocol:         "default",
		LimitsCurrency:           "RUB",
		MaxPaymentAmount:         15000,
		MinPaymentAmount:         1,
		Name:                     map[string]string{"en": "test project 1"},
		IsProductsCheckout:       true,
		AllowDynamicRedirectUrls: true,
		SecretKey:                "test project 1 secret key",
		Status:                   pkg.ProjectStatusInProduction,
		MerchantId:               merchant.Id,
	}
	projectXsolla := &billing.Project{
		Id:                 primitive.NewObjectID().Hex(),
		MerchantId:         primitive.NewObjectID().Hex(),
		CallbackCurrency:   "RUB",
		CallbackProtocol:   "xsolla",
		LimitsCurrency:     "RUB",
		MaxPaymentAmount:   15000,
		MinPaymentAmount:   0,
		Name:               map[string]string{"en": "test project 2"},
		IsProductsCheckout: true,
		SecretKey:          "test project 2 secret key",
		Status:             pkg.ProjectStatusInProduction,
	}
	projectCardpay := &billing.Project{
		Id:                 primitive.NewObjectID().Hex(),
		MerchantId:         primitive.NewObjectID().Hex(),
		CallbackCurrency:   "RUB",
		CallbackProtocol:   "cardpay",
		LimitsCurrency:     "RUB",
		MaxPaymentAmount:   15000,
		MinPaymentAmount:   0,
		Name:               map[string]string{"en": "test project 3"},
		IsProductsCheckout: true,
		SecretKey:          "test project 3 secret key",
		Status:             pkg.ProjectStatusInProduction,
	}

	pmQiwi := &billing.PaymentMethod{
		Id:               primitive.NewObjectID().Hex(),
		Name:             "Qiwi",
		Group:            "QIWI",
		MinPaymentAmount: 0,
		MaxPaymentAmount: 0,
		ExternalId:       "QIWI",
		TestSettings: map[string]*billing.PaymentMethodParams{
			"RUB": {
				Currency:   "RUB",
				TerminalId: "15993",
			},
		},
		Type:            "ewallet",
		IsActive:        true,
		PaymentSystemId: ps.Id,
	}
	pmBitcoin := &billing.PaymentMethod{
		Id:               primitive.NewObjectID().Hex(),
		Name:             "Bitcoin",
		Group:            "BITCOIN",
		MinPaymentAmount: 0,
		MaxPaymentAmount: 0,
		ExternalId:       "BITCOIN",
		TestSettings: map[string]*billing.PaymentMethodParams{
			"RUB": {
				Currency:   "RUB",
				TerminalId: "16007",
			},
		},
		Type:            "crypto",
		IsActive:        true,
		PaymentSystemId: ps.Id,
	}

	projects := []*billing.Project{
		projectDefault,
		projectXsolla,
		projectCardpay,
	}

	pms := []*billing.PaymentMethod{pmBankCard, pmQiwi, pmBitcoin}
	if err := suite.service.paymentMethod.MultipleInsert(context.TODO(), pms); err != nil {
		suite.FailNow("Insert payment methods test data failed", "%v", err)
	}

	if err := suite.service.merchant.Insert(context.TODO(), merchant); err != nil {
		suite.FailNow("Insert merchant test data failed", "%v", err)
	}

	if err := suite.service.country.Insert(context.TODO(), country); err != nil {
		suite.FailNow("Insert country test data failed", "%v", err)
	}

	if err := suite.service.project.MultipleInsert(context.TODO(), projects); err != nil {
		suite.FailNow("Insert project test data failed", "%v", err)
	}

	suite.exCh = make(chan bool, 1)
	suite.project = projectDefault
}

func (suite *BillingServiceTestSuite) TearDownTest() {
	err := suite.service.db.Drop()

	if err != nil {
		suite.FailNow("Database deletion failed", "%v", err)
	}

	err = suite.service.db.Close()

	if err != nil {
		suite.FailNow("Database close failed", "%v", err)
	}
}

func (suite *BillingServiceTestSuite) TestNewBillingService() {
	redisdb := mocks.NewTestRedis()
	suite.cache, _ = NewCacheRedis(redisdb, "cache")
	service := NewBillingService(
		suite.db,
		suite.cfg,
		nil,
		nil,
		nil,
		nil,
		nil,
		suite.cache,
		mocks.NewCurrencyServiceMockOk(),
		mocks.NewDocumentSignerMockOk(),
		&reportingMocks.ReporterService{},
		mocks.NewFormatterOK(),
		mocks.NewBrokerMockOk(),
		&casbinMocks.CasbinService{},
	)

	err := service.Init()
	assert.Nil(suite.T(), err)
}

func (suite *BillingServiceTestSuite) TestBillingService_AccountingCurrencyInitError() {
	cfg, err := config.NewConfig()

	assert.NoError(suite.T(), err)

	suite.cache, err = NewCacheRedis(mocks.NewTestRedis(), "cache")
	service := NewBillingService(
		suite.db,
		cfg,
		nil,
		nil,
		nil,
		nil,
		nil,
		suite.cache,
		mocks.NewCurrencyServiceMockError(),
		mocks.NewDocumentSignerMockOk(),
		&reportingMocks.ReporterService{},
		mocks.NewFormatterOK(),
		mocks.NewBrokerMockOk(),
		&casbinMocks.CasbinService{},
	)

	err = service.Init()
	assert.Error(suite.T(), err)
}

func (suite *BillingServiceTestSuite) TestBillingService_IsProductionEnvironment() {
	redisdb := mocks.NewTestRedis()
	suite.cache, _ = NewCacheRedis(redisdb, "cache")
	service := NewBillingService(
		suite.db,
		suite.cfg,
		nil,
		nil,
		nil,
		nil,
		nil,
		suite.cache,
		mocks.NewCurrencyServiceMockOk(),
		mocks.NewDocumentSignerMockOk(),
		&reportingMocks.ReporterService{},
		mocks.NewFormatterOK(),
		mocks.NewBrokerMockOk(),
		&casbinMocks.CasbinService{},
	)

	err := service.Init()
	assert.Nil(suite.T(), err)
}

func (suite *BillingServiceTestSuite) TestBillingService_CheckProjectRequestSignature_Ok() {
	req := &grpc.CheckProjectRequestSignatureRequest{
		Body:      `{"field1": "val1", "field2": "val2", "field3": "val3"}`,
		ProjectId: suite.project.Id,
	}
	rsp := &grpc.CheckProjectRequestSignatureResponse{}

	req.Signature = suite.project.SecretKey

	err := suite.service.CheckProjectRequestSignature(context.TODO(), req, rsp)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), pkg.ResponseStatusOk, rsp.Status)
}

func (suite *BillingServiceTestSuite) TestBillingService_CheckProjectRequestSignature_ProjectNotFound_Error() {
	req := &grpc.CheckProjectRequestSignatureRequest{
		Body:      `{"field1": "val1", "field2": "val2", "field3": "val3"}`,
		ProjectId: primitive.NewObjectID().Hex(),
	}
	rsp := &grpc.CheckProjectRequestSignatureResponse{}

	err := suite.service.CheckProjectRequestSignature(context.TODO(), req, rsp)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), pkg.ResponseStatusBadData, rsp.Status)
	assert.Equal(suite.T(), orderErrorProjectNotFound, rsp.Message)
}

func (suite *BillingServiceTestSuite) TestBillingService_CheckProjectRequestSignature_IncorrectSignature_Error() {
	req := &grpc.CheckProjectRequestSignatureRequest{
		Body:      `{"field1": "val1", "field2": "val2", "field3": "val3"}`,
		ProjectId: suite.project.Id,
	}
	rsp := &grpc.CheckProjectRequestSignatureResponse{}

	req.Signature = "some_random_string"

	err := suite.service.CheckProjectRequestSignature(context.TODO(), req, rsp)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), pkg.ResponseStatusBadData, rsp.Status)
	assert.Equal(suite.T(), orderErrorSignatureInvalid, rsp.Message)
}
