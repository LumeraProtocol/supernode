package dd

import (
	"context"
	"fmt"

	ddService "github.com/LumeraProtocol/dd-service"
	"github.com/LumeraProtocol/supernode/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/pkg/net"
)

type RarenessScoreRequest struct {
	Filepath string
}

type ImageRarenessScoreResponse struct {
	PastelBlockHashWhenRequestSubmitted           string
	PastelBlockHeightWhenRequestSubmitted         string
	UtcTimestampWhenRequestSubmitted              string
	PastelIdOfSubmitter                           string
	PastelIdOfRegisteringSupernode_1              string
	PastelIdOfRegisteringSupernode_2              string
	PastelIdOfRegisteringSupernode_3              string
	IsPastelOpenapiRequest                        bool
	ImageFilePath                                 string
	DupeDetectionSystemVersion                    string
	IsLikelyDupe                                  bool
	IsRareOnInternet                              bool
	OverallRarenessScore                          float32
	PctOfTop_10MostSimilarWithDupeProbAbove_25Pct float32
	PctOfTop_10MostSimilarWithDupeProbAbove_33Pct float32
	PctOfTop_10MostSimilarWithDupeProbAbove_50Pct float32
	RarenessScoresTableJsonCompressedB64          string
	InternetRareness                              *ddService.InternetRareness
	OpenNsfwScore                                 float32
	AlternativeNsfwScores                         *ddService.AltNsfwScores
	ImageFingerprintOfCandidateImageFile          []float64
	CollectionNameString                          string
	HashOfCandidateImageFile                      string
	OpenApiGroupIdString                          string
	GroupRarenessScore                            float32
	CandidateImageThumbnailWebpAsBase64String     string
	DoesNotImpactTheFollowingCollectionStrings    string
	IsInvalidSenseRequest                         bool
	InvalidSenseRequestReason                     string
	SimilarityScoreToFirstEntryInCollection       float32
	CpProbability                                 float32
	ChildProbability                              float32
	ImageFingerprintSetChecksum                   string
}

// ImageRarenessScore gets the image rareness score
func (c *Client) ImageRarenessScore(ctx context.Context, req RarenessScoreRequest) (ImageRarenessScoreResponse, error) {
	ctx = net.AddCorrelationID(ctx)
	fields := logtrace.Fields{
		logtrace.FieldMethod:  "ImageRarenessScore",
		logtrace.FieldRequest: req,
	}
	logtrace.Info(ctx, "getting image rareness score", fields)

	res, err := c.ddService.ImageRarenessScore(ctx, &ddService.RarenessScoreRequest{ImageFilepath: req.Filepath})
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		logtrace.Error(ctx, "failed to get image rareness score", fields)
		return ImageRarenessScoreResponse{}, fmt.Errorf("dd image rareness score error: %w", err)
	}

	logtrace.Info(ctx, "successfully got image rareness score", fields)
	return toImageRarenessScoreResponse(res), nil
}

func toImageRarenessScoreResponse(reply *ddService.ImageRarenessScoreReply) ImageRarenessScoreResponse {
	return ImageRarenessScoreResponse{
		PastelBlockHashWhenRequestSubmitted:           reply.PastelBlockHashWhenRequestSubmitted,
		PastelBlockHeightWhenRequestSubmitted:         reply.PastelBlockHeightWhenRequestSubmitted,
		UtcTimestampWhenRequestSubmitted:              reply.UtcTimestampWhenRequestSubmitted,
		PastelIdOfSubmitter:                           reply.PastelIdOfSubmitter,
		PastelIdOfRegisteringSupernode_1:              reply.PastelIdOfRegisteringSupernode_1,
		PastelIdOfRegisteringSupernode_2:              reply.PastelIdOfRegisteringSupernode_2,
		PastelIdOfRegisteringSupernode_3:              reply.PastelIdOfRegisteringSupernode_3,
		IsPastelOpenapiRequest:                        reply.IsPastelOpenapiRequest,
		ImageFilePath:                                 reply.ImageFilePath,
		DupeDetectionSystemVersion:                    reply.DupeDetectionSystemVersion,
		IsLikelyDupe:                                  reply.IsLikelyDupe,
		IsRareOnInternet:                              reply.IsRareOnInternet,
		OverallRarenessScore:                          reply.OverallRarenessScore,
		PctOfTop_10MostSimilarWithDupeProbAbove_25Pct: reply.PctOfTop_10MostSimilarWithDupeProbAbove_25Pct,
		PctOfTop_10MostSimilarWithDupeProbAbove_33Pct: reply.PctOfTop_10MostSimilarWithDupeProbAbove_33Pct,
		PctOfTop_10MostSimilarWithDupeProbAbove_50Pct: reply.PctOfTop_10MostSimilarWithDupeProbAbove_50Pct,
		RarenessScoresTableJsonCompressedB64:          reply.RarenessScoresTableJsonCompressedB64,
		InternetRareness:                              reply.InternetRareness,
		OpenNsfwScore:                                 reply.OpenNsfwScore,
		AlternativeNsfwScores:                         reply.AlternativeNsfwScores,
		ImageFingerprintOfCandidateImageFile:          reply.ImageFingerprintOfCandidateImageFile,
		CollectionNameString:                          reply.CollectionNameString,
		HashOfCandidateImageFile:                      reply.HashOfCandidateImageFile,
		OpenApiGroupIdString:                          reply.OpenApiGroupIdString,
		GroupRarenessScore:                            reply.GroupRarenessScore,
		CandidateImageThumbnailWebpAsBase64String:     reply.CandidateImageThumbnailWebpAsBase64String,
		DoesNotImpactTheFollowingCollectionStrings:    reply.DoesNotImpactTheFollowingCollectionStrings,
		IsInvalidSenseRequest:                         reply.IsInvalidSenseRequest,
		InvalidSenseRequestReason:                     reply.InvalidSenseRequestReason,
		SimilarityScoreToFirstEntryInCollection:       reply.SimilarityScoreToFirstEntryInCollection,
		CpProbability:                                 reply.CpProbability,
		ChildProbability:                              reply.ChildProbability,
		ImageFingerprintSetChecksum:                   reply.ImageFingerprintSetChecksum,
	}
}
