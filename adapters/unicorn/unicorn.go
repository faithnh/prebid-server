package unicorn

import (
  "encoding/json"
  "fmt"
  "net/http"

  "github.com/buger/jsonparser"
  "github.com/mxmCherry/openrtb"
  "github.com/prebid/prebid-server/adapters"
  "github.com/prebid/prebid-server/config"
  "github.com/prebid/prebid-server/errortypes"
  "github.com/prebid/prebid-server/openrtb_ext"
)

const (
  unicornDefaultCurrency = "JPY"
  unicornAuctionType = 1
)

// UnicornAdapter describes a Smaato prebid server adapter.
type UnicornAdapter struct {
  endpoint string
}

// unicornImpExt is imp ext for UNICORN
type unicornImpExt struct {
  Bidder openrtb_ext.ExtImpUnicorn `json:"bidder"`
}

// unicornSourceExt is source ext for UNICORN
type unicornSourceExt struct {
  Stype  string `json:"stype"`
  Bidder string `json"bidder"`
}

// unicornExt is ext for UNICORN
type unicornExt struct {
  Prebid    *openrtb_ext.ExtImpPrebid `json:"prebid,omitempty"`
  AccountID int64                     `json:"accountId,omitempty"`
}

// Builder builds a new instance of the Foo adapter for the given bidder with the given config.
func Builder(bidderName openrtb_ext.BidderName, config config.Adapter) (adapters.Bidder, error) {
  bidder := &UnicornAdapter{
    endpoint: config.Endpoint,
  }
  return bidder, nil
}

// MakeRequests makes the HTTP requests which should be made to fetch bids.
func (a *UnicornAdapter) MakeRequests(request *openrtb.BidRequest, requestInfo *adapters.ExtraRequestInfo) ([]*adapters.RequestData, []error) {
  var extRegs openrtb_ext.ExtRegs
  if request.Regs != nil {
    if request.Regs.COPPA == 1 {
      return nil, []error{}
    }
    if err := json.Unmarshal(request.Regs.Ext, &extRegs); err == nil {
      if extRegs.GDPR != nil && (*extRegs.GDPR == 1) {
        return nil, []error{}
      }
      if extRegs.USPrivacy != "" {
        return nil, []error{}
      }
    }
  }

  request.AT = unicornAuctionType

  imp, err := setImps(request, requestInfo)
  if err != nil {
    return nil, []error{err}
  }

  request.Imp = imp
  request.Cur = []string{unicornDefaultCurrency}

  request.Source.Ext, err = setSourceExt()
  if err != nil {
    return nil, []error{err}
  }

  request.Ext, err = setExt(request)
  if err != nil {
    return nil, []error{err}
  }

  requestJSON, err := json.Marshal(request)
  if err != nil {
    return nil, []error{err}
  }

  requestData := &adapters.RequestData{
    Method:  "POST",
    Uri:     a.endpoint,
    Body:    requestJSON,
  }

  return []*adapters.RequestData{requestData}, nil
}

func setImps(request *openrtb.BidRequest, requestInfo *adapters.ExtraRequestInfo) ([]openrtb.Imp, error) {
  for i := 0; i < len(request.Imp); i++ {
    imp := &request.Imp[i]

    var ext unicornImpExt
    err := json.Unmarshal(imp.Ext, &ext)

    if err != nil {
      return nil, &errortypes.BadInput{
			  Message: fmt.Sprintf("Error while decoding imp[%d].ext, err: %s", i, err),
		  }
    }

    var placementID string
    if ext.Bidder.PlacementID == "" {
      placementID = imp.TagID
    } else {
      placementID = ext.Bidder.PlacementID
    }
    ext.Bidder.PlacementID = placementID

    imp.Ext, err = json.Marshal(ext)
    if err != nil {
      return nil, &errortypes.BadInput{
			  Message: fmt.Sprintf("Error while encoding imp[%d].ext, err: %s", i, err),
		  }
    }

    secure := int8(1)
    imp.Secure = &secure
    imp.TagID = placementID
  }
  return request.Imp, nil
}

func setSourceExt() (json.RawMessage, error) {
  siteExt, err := json.Marshal(unicornSourceExt{
    Stype: "prebid_uncn",
    Bidder: "unicorn",
  })
  if err != nil {
    return nil, &errortypes.BadInput{
		  Message: fmt.Sprintf("Error while encoding source.ext, err: %s", err),
		}
  }
  return siteExt, nil
}

func setExt(request *openrtb.BidRequest) (json.RawMessage, error) {
  accountID, err := jsonparser.GetInt(request.Imp[0].Ext, "bidder", "accountId")
  if err != nil {
    accountID = 0
  }
  var decodedExt *unicornExt
  err = json.Unmarshal(request.Ext, &decodedExt)
  if err != nil {
    return nil, &errortypes.BadInput{
			Message: fmt.Sprintf("Error while decoding ext, err: %s", err),
		}
  }
  decodedExt.AccountID = accountID

  ext, err := json.Marshal(decodedExt)
  if err != nil {
    return nil, &errortypes.BadInput{
			Message: fmt.Sprintf("Error while encoding ext, err: %s", err),
		}
  }
  return ext, nil
}

// MakeBids unpacks the server's response into Bids.
func (a *UnicornAdapter) MakeBids(request *openrtb.BidRequest, requestData *adapters.RequestData, responseData *adapters.ResponseData) (*adapters.BidderResponse, []error) {

  if responseData.StatusCode == http.StatusNoContent {
    return nil, nil
  }

  if responseData.StatusCode == http.StatusBadRequest {
    err := &errortypes.BadInput{
      Message: "Unexpected status code: 400. Bad request from publisher. Run with request.debug = 1 for more info.",
    }
    return nil, []error{err}
  }

  if responseData.StatusCode != http.StatusOK {
    err := &errortypes.BadServerResponse{
      Message: fmt.Sprintf("Unexpected status code: %d. Run with request.debug = 1 for more info.", responseData.StatusCode),
    }
    return nil, []error{err}
  }

  var response openrtb.BidResponse
  if err := json.Unmarshal(responseData.Body, &response); err != nil {
    return nil, []error{err}
  }

  bidResponse := adapters.NewBidderResponseWithBidsCapacity(len(request.Imp))
  bidResponse.Currency = response.Cur
  for _, seatBid := range response.SeatBid {
    for _, bid := range seatBid.Bid {
      var bidType openrtb_ext.BidType
      for _, imp := range request.Imp {
        if imp.ID == bid.ImpID {
          if imp.Banner != nil {
            bidType = openrtb_ext.BidTypeBanner
          }
          if imp.Native != nil {
            bidType = openrtb_ext.BidTypeNative
          }
        }
      }
      b := &adapters.TypedBid{
        Bid:     &bid,
        BidType: bidType,
      }
      bidResponse.Bids = append(bidResponse.Bids, b)
    }
  }
  return bidResponse, nil
}