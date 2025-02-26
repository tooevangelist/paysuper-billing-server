package service

import (
	"context"
	"github.com/go-redis/redis"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/mongodb"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/golang/protobuf/ptypes"
	"github.com/jinzhu/now"
	casbinMocks "github.com/paysuper/casbin-server/pkg/mocks"
	"github.com/paysuper/paysuper-billing-server/internal/config"
	"github.com/paysuper/paysuper-billing-server/internal/database"
	"github.com/paysuper/paysuper-billing-server/internal/mocks"
	"github.com/paysuper/paysuper-billing-server/pkg"
	"github.com/paysuper/paysuper-billing-server/pkg/proto/billing"
	"github.com/paysuper/paysuper-billing-server/pkg/proto/grpc"
	reportingMocks "github.com/paysuper/paysuper-reporter/pkg/mocks"
	"github.com/stretchr/testify/assert"
	mock2 "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.uber.org/zap"
	rabbitmq "gopkg.in/ProtocolONE/rabbitmq.v1/pkg"
	mongodb "gopkg.in/paysuper/paysuper-database-mongo.v1"
	"testing"
	"time"
)

type VatReportsTestSuite struct {
	suite.Suite
	service *Service
	log     *zap.Logger
	cache   CacheInterface

	projectFixedAmount *billing.Project
	paymentMethod      *billing.PaymentMethod
	paymentSystem      *billing.PaymentSystem
}

func Test_VatReports(t *testing.T) {
	suite.Run(t, new(VatReportsTestSuite))
}

func (suite *VatReportsTestSuite) SetupTest() {
	cfg, err := config.NewConfig()
	if err != nil {
		suite.FailNow("Config load failed", "%v", err)
	}
	cfg.CardPayApiUrl = "https://sandbox.cardpay.com"
	cfg.OrderViewUpdateBatchSize = 20

	m, err := migrate.New(
		"file://../../migrations/tests",
		cfg.MongoDsn)
	assert.NoError(suite.T(), err, "Migrate init failed")

	err = m.Up()
	if err != nil && err.Error() != "no change" {
		suite.FailNow("Migrations failed", "%v", err)
	}

	ctx, _ := context.WithTimeout(context.Background(), 60*time.Second)
	opts := []mongodb.Option{mongodb.Context(ctx)}
	db, err := mongodb.NewDatabase(opts...)
	if err != nil {
		suite.FailNow("Database connection failed", "%v", err)
	}

	suite.log, err = zap.NewProduction()

	if err != nil {
		suite.FailNow("Logger initialization failed", "%v", err)
	}

	broker, err := rabbitmq.NewBroker(cfg.BrokerAddress)

	if err != nil {
		suite.FailNow("Creating RabbitMQ publisher failed", "%v", err)
	}

	redisClient := database.NewRedis(
		&redis.Options{
			Addr:     cfg.RedisHost,
			Password: cfg.RedisPassword,
		},
	)

	redisdb := mocks.NewTestRedis()
	suite.cache, err = NewCacheRedis(redisdb, "cache")
	suite.service = NewBillingService(
		db,
		cfg,
		mocks.NewGeoIpServiceTestOk(),
		mocks.NewRepositoryServiceOk(),
		mocks.NewTaxServiceOkMock(),
		broker,
		redisClient,
		suite.cache,
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

	_, suite.projectFixedAmount, suite.paymentMethod, suite.paymentSystem = helperCreateEntitiesForTests(suite.Suite, suite.service)
}

func (suite *VatReportsTestSuite) TearDownTest() {
	err := suite.service.db.Drop()

	if err != nil {
		suite.FailNow("Database deletion failed", "%v", err)
	}

	err = suite.service.db.Close()

	if err != nil {
		suite.FailNow("Database close failed", "%v", err)
	}
}

func (suite *VatReportsTestSuite) TestVatReports_getLastVatReportTime() {
	_, _, err := suite.service.getLastVatReportTime(0)
	assert.Error(suite.T(), err)

	from, to, err := suite.service.getLastVatReportTime(int32(3))
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), from, now.BeginningOfQuarter())
	assert.Equal(suite.T(), to, now.EndOfQuarter())

	from, to, err = suite.service.getLastVatReportTime(int32(1))
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), from, now.BeginningOfMonth())
	assert.Equal(suite.T(), to, now.EndOfMonth())

	fromRef := now.BeginningOfMonth()
	toRef := now.EndOfMonth()

	if fromRef.Month()%2 == 0 {
		fromRef = fromRef.AddDate(0, -1, 0)
	} else {
		toRef = toRef.AddDate(0, 1, 0)
		toRef = now.New(toRef).EndOfMonth()
	}

	from, to, err = suite.service.getLastVatReportTime(int32(2))
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), from, fromRef)
	assert.Equal(suite.T(), to, toRef)
	assert.Equal(suite.T(), fromRef.Month()%2, time.Month(1))
	assert.Equal(suite.T(), toRef.Month()%2, time.Month(0))
}

func (suite *VatReportsTestSuite) TestVatReports_getVatReportTimeForDate() {

	t, err := time.Parse(time.RFC3339, "2019-06-29T11:45:26.371Z")
	assert.NoError(suite.T(), err)

	from, to, err := suite.service.getVatReportTimeForDate(int32(3), t)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), from.Format(time.RFC3339), "2019-04-01T00:00:00Z")
	assert.Equal(suite.T(), to.Format(time.RFC3339), "2019-06-30T23:59:59Z")

	from, to, err = suite.service.getVatReportTimeForDate(int32(1), t)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), from.Format(time.RFC3339), "2019-06-01T00:00:00Z")
	assert.Equal(suite.T(), to.Format(time.RFC3339), "2019-06-30T23:59:59Z")

	from, to, err = suite.service.getVatReportTimeForDate(int32(2), t)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), from.Format(time.RFC3339), "2019-05-01T00:00:00Z")
	assert.Equal(suite.T(), to.Format(time.RFC3339), "2019-06-30T23:59:59Z")

	t, err = time.Parse(time.RFC3339, "2019-05-29T11:45:26.371Z")
	assert.NoError(suite.T(), err)
	from, to, err = suite.service.getVatReportTimeForDate(int32(2), t)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), from.Format(time.RFC3339), "2019-05-01T00:00:00Z")
	assert.Equal(suite.T(), to.Format(time.RFC3339), "2019-06-30T23:59:59Z")

	t, err = time.Parse(time.RFC3339, "2019-07-29T11:45:26.371Z")
	assert.NoError(suite.T(), err)
	from, to, err = suite.service.getVatReportTimeForDate(int32(2), t)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), from.Format(time.RFC3339), "2019-07-01T00:00:00Z")
	assert.Equal(suite.T(), to.Format(time.RFC3339), "2019-08-31T23:59:59Z")

	t, err = time.Parse(time.RFC3339, "2019-08-29T11:45:26.371Z")
	assert.NoError(suite.T(), err)
	from, to, err = suite.service.getVatReportTimeForDate(int32(2), t)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), from.Format(time.RFC3339), "2019-07-01T00:00:00Z")
	assert.Equal(suite.T(), to.Format(time.RFC3339), "2019-08-31T23:59:59Z")

	t, err = time.Parse(time.RFC3339, "2019-04-01T00:00:00Z")
	assert.NoError(suite.T(), err)
	from, to, err = suite.service.getVatReportTimeForDate(int32(3), t)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), from.Format(time.RFC3339), "2019-04-01T00:00:00Z")
	assert.Equal(suite.T(), to.Format(time.RFC3339), "2019-06-30T23:59:59Z")

	t, err = time.Parse(time.RFC3339, "2019-06-30T23:59:59Z")
	assert.NoError(suite.T(), err)
	from, to, err = suite.service.getVatReportTimeForDate(int32(3), t)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), from.Format(time.RFC3339), "2019-04-01T00:00:00Z")
	assert.Equal(suite.T(), to.Format(time.RFC3339), "2019-06-30T23:59:59Z")
}

func (suite *VatReportsTestSuite) TestVatReports_ProcessVatReports() {
	amounts := []float64{100, 10}
	currencies := []string{"RUB", "USD"}
	countries := []string{"RU", "FI"}
	var orders []*billing.Order
	numberOfOrders := 30

	count := 0
	for count < numberOfOrders {
		order := helperCreateAndPayOrder(
			suite.Suite,
			suite.service,
			amounts[count%2],
			currencies[count%2],
			countries[count%2],
			suite.projectFixedAmount,
			suite.paymentMethod,
		)
		assert.NotNil(suite.T(), order)
		orders = append(orders, order)

		count++
	}

	suite.paymentSystem.Handler = "mock_ok"
	err := suite.service.paymentSystem.Update(context.TODO(), suite.paymentSystem)
	assert.NoError(suite.T(), err)

	for _, order := range orders {
		refund := helperMakeRefund(suite.Suite, suite.service, order, order.ChargeAmount*0.5, false)
		assert.NotNil(suite.T(), refund)
	}

	req := &grpc.ProcessVatReportsRequest{
		Date: ptypes.TimestampNow(),
	}
	err = suite.service.ProcessVatReports(context.TODO(), req, &grpc.EmptyResponse{})
	assert.NoError(suite.T(), err)

	repRes := grpc.VatReportsResponse{}

	err = suite.service.GetVatReportsForCountry(context.TODO(), &grpc.VatReportsRequest{Country: "RU"}, &repRes)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), repRes.Status, pkg.ResponseStatusOk)
	assert.NotNil(suite.T(), repRes.Data)
	assert.Equal(suite.T(), repRes.Data.Count, int32(1))

	report := repRes.Data.Items[0]
	assert.NotNil(suite.T(), report)
	assert.Equal(suite.T(), report.Country, "RU")
	assert.Equal(suite.T(), report.Currency, "RUB")
	assert.EqualValues(suite.T(), report.TransactionsCount, numberOfOrders)
	assert.EqualValues(suite.T(), report.GrossRevenue, 900)
	assert.EqualValues(suite.T(), report.VatAmount, 150)
	assert.EqualValues(suite.T(), report.FeesAmount, 144.38)
	assert.EqualValues(suite.T(), report.DeductionAmount, 0)
	assert.EqualValues(suite.T(), report.CountryAnnualTurnover, 1800)
	assert.EqualValues(suite.T(), report.WorldAnnualTurnover, 13183.1)
	assert.Equal(suite.T(), report.Status, pkg.VatReportStatusThreshold)

	err = suite.service.GetVatReportsForCountry(context.TODO(), &grpc.VatReportsRequest{Country: "FI"}, &repRes)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), repRes.Status, pkg.ResponseStatusOk)
	assert.NotNil(suite.T(), repRes.Data)
	assert.Equal(suite.T(), repRes.Data.Count, int32(1))

	report = repRes.Data.Items[0]
	assert.NotNil(suite.T(), report)
	assert.Equal(suite.T(), report.Country, "FI")
	assert.Equal(suite.T(), report.Currency, "EUR")
	assert.EqualValues(suite.T(), report.TransactionsCount, numberOfOrders)
	assert.EqualValues(suite.T(), report.GrossRevenue, 81.31)
	assert.EqualValues(suite.T(), report.VatAmount, 13.55)
	assert.EqualValues(suite.T(), report.FeesAmount, 8.89)
	assert.EqualValues(suite.T(), report.DeductionAmount, 0)
	assert.EqualValues(suite.T(), report.CountryAnnualTurnover, 162)
	assert.EqualValues(suite.T(), report.WorldAnnualTurnover, 188.33)
	assert.Equal(suite.T(), report.Status, pkg.VatReportStatusThreshold)

	assert.NoError(suite.T(), err)
}

func (suite *VatReportsTestSuite) TestVatReports_PaymentDateSet() {

	ci := &mocks.CentrifugoInterface{}
	ci.On("Publish", mock2.Anything, mock2.Anything, mock2.Anything).Return(nil)
	suite.service.centrifugo = ci

	nowTimestamp := time.Now().Unix()

	vatReport := &billing.VatReport{
		Id:                    primitive.NewObjectID().Hex(),
		Country:               "RU",
		VatRate:               20,
		Currency:              "RUB",
		Status:                pkg.VatReportStatusNeedToPay,
		TransactionsCount:     999,
		GrossRevenue:          100500,
		VatAmount:             100500,
		FeesAmount:            0,
		DeductionAmount:       0,
		CountryAnnualTurnover: 100500,
		WorldAnnualTurnover:   100500,
		CorrectionAmount:      0,
		AmountsApproximate:    false,
		DateFrom:              ptypes.TimestampNow(),
		DateTo:                ptypes.TimestampNow(),
		CreatedAt:             ptypes.TimestampNow(),
		UpdatedAt:             ptypes.TimestampNow(),
		PayUntilDate:          ptypes.TimestampNow(),
	}

	err := suite.service.insertVatReport(context.TODO(), vatReport)
	assert.NoError(suite.T(), err)

	oid, err := primitive.ObjectIDFromHex(vatReport.Id)
	assert.NoError(suite.T(), err)
	query := bson.M{
		"_id": oid,
	}
	var vr *billing.VatReport
	err = suite.service.db.Collection(collectionVatReports).FindOne(context.TODO(), query).Decode(&vr)
	assert.NoError(suite.T(), err)
	assert.NotEqual(suite.T(), vr.Status, pkg.VatReportStatusPaid)
	assert.EqualValues(suite.T(), -62135596800, vr.PaidAt.Seconds)

	req := &grpc.UpdateVatReportStatusRequest{
		Id:     vr.Id,
		Status: pkg.VatReportStatusPaid,
	}
	res := &grpc.ResponseError{}
	err = suite.service.UpdateVatReportStatus(context.TODO(), req, res)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), res.Status, pkg.ResponseStatusOk)
	assert.Empty(suite.T(), res.Message)

	err = suite.service.db.Collection(collectionVatReports).FindOne(context.TODO(), query).Decode(&vr)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), vr.Status, pkg.VatReportStatusPaid)
	assert.GreaterOrEqual(suite.T(), nowTimestamp, vr.PaidAt.Seconds)
}
