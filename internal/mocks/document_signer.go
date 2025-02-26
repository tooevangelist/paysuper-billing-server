package mocks

import (
	"github.com/golang/protobuf/ptypes"
	"github.com/paysuper/document-signer/pkg/proto"
	"github.com/paysuper/paysuper-billing-server/pkg"
	mock2 "github.com/stretchr/testify/mock"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"time"
)

var (
	CreateSignatureResponse = &proto.CreateSignatureResponse{
		Status: pkg.ResponseStatusOk,
		Item: &proto.CreateSignatureResponseItem{
			DetailsUrl:          "http:/127.0.0.1",
			FilesUrl:            "http:/127.0.0.1",
			SignatureRequestId:  primitive.NewObjectID().Hex(),
			MerchantSignatureId: primitive.NewObjectID().Hex(),
			PsSignatureId:       primitive.NewObjectID().Hex(),
		},
	}
	GetSignatureUrlResponse = &proto.GetSignatureUrlResponse{
		Status: pkg.ResponseStatusOk,
		Item: &proto.GetSignatureUrlResponseEmbedded{
			SignUrl: "http://127.0.0.1",
		},
	}
)

func NewDocumentSignerMockOk() proto.DocumentSignerService {
	GetSignatureUrlResponse.Item.ExpiresAt, _ = ptypes.TimestampProto(time.Now().Add(time.Duration(1 * time.Hour)))

	ds := &DocumentSignerService{}
	ds.On("CreateSignature", mock2.Anything, mock2.Anything, mock2.Anything).
		Return(CreateSignatureResponse, nil)
	ds.On("GetSignatureUrl", mock2.Anything, mock2.Anything).Return(GetSignatureUrlResponse, nil)

	return ds
}
