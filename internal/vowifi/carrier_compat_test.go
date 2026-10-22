package vowifi

import (
	"slices"
	"strings"
	"testing"
)

func TestResolveCarrierProfileUsesStandardDefault(t *testing.T) {
	profile := ResolveCarrierProfile(SIMIdentity{
		IMSI: "999010000000001", HomeMCC: "999", HomeMNC: "01",
	})
	if profile.ID != CarrierProfileStandard || profile.MatchSource != "standard" {
		t.Fatalf("default profile = %#v", profile)
	}
	if profile.IKEProposal != IKEProposalModern || !profile.AdvertiseEAPOnly ||
		profile.IMSIdentityProfile != IMSProfileStandard || profile.IMSRegisterProfile != IMSProfileStandard {
		t.Fatalf("default profile lost standard capabilities: %#v", profile)
	}
}

func TestCarrierProfileSubscriberIMSIRewriteValidation(t *testing.T) {
	rewrite := []byte(`{"version":1,"profiles":[{"id":"subscriber-rewrite","match":{"iccid_prefixes":["89636626"]},"identity":{"subscriber_imsi_rewrite":{"from_prefix":"204047616","to_prefix":"515661015"}}}]}`)
	rules, err := loadCarrierProfiles(rewrite)
	if err != nil {
		t.Fatal(err)
	}
	profile := applyCarrierProfileRule(defaultCarrierProfile(), rules[0], "iccid", SIMIdentity{})
	if got := profile.EffectiveSubscriberIMSI("204047616000001"); got != "515661015000001" {
		t.Fatalf("rewritten subscriber IMSI = %q", got)
	}
	if got := profile.EffectiveSubscriberIMSI("204041234567890"); got != "204041234567890" {
		t.Fatalf("unmatched subscriber IMSI changed to %q", got)
	}
	multiple := []byte(`{"version":1,"profiles":[{"id":"subscriber-rewrites","match":{"iccid_prefixes":["896366"]},"identity":{"subscriber_imsi_rewrites":[{"from_prefix":"204047616","to_prefix":"515661015"},{"from_prefix":"204043610","to_prefix":"515661037"}]}}]}`)
	rules, err = loadCarrierProfiles(multiple)
	if err != nil {
		t.Fatal(err)
	}
	profile = applyCarrierProfileRule(defaultCarrierProfile(), rules[0], "iccid", SIMIdentity{})
	if got := profile.EffectiveSubscriberIMSI("204043610000001"); got != "515661037000001" {
		t.Fatalf("second subscriber rewrite = %q", got)
	}
	if got, source := EffectiveSubscriberIdentity(SIMIdentity{IMSI: "204043610000001", ProvisionedIMSI: "515661037000009"}, profile); got != "515661037000009" || source != "provisioned_imsi" {
		t.Fatalf("provisioned subscriber identity = %q, %q", got, source)
	}
	for _, malformed := range []string{
		`{"version":1,"profiles":[{"id":"missing-target","match":{"iccid_prefixes":["896366"]},"identity":{"subscriber_imsi_rewrite":{"from_prefix":"204047616"}}}]}`,
		`{"version":1,"profiles":[{"id":"length-mismatch","match":{"iccid_prefixes":["896366"]},"identity":{"subscriber_imsi_rewrite":{"from_prefix":"204047616","to_prefix":"51566"}}}]}`,
		`{"version":1,"profiles":[{"id":"non-decimal","match":{"iccid_prefixes":["896366"]},"identity":{"subscriber_imsi_rewrite":{"from_prefix":"20404x616","to_prefix":"515661015"}}}]}`,
	} {
		if _, err := loadCarrierProfiles([]byte(malformed)); err == nil {
			t.Fatalf("invalid subscriber rewrite was accepted: %s", malformed)
		}
	}
}

func TestBuiltinDITOProfileRewritesRoamingSubscriberPrefix(t *testing.T) {
	for _, test := range []struct {
		homeMCC, homeMNC string
		imsi, want       string
	}{
		{"515", "66", "204047616000001", "515661015000001"},
		{"204", "04", "204047616000001", "515661015000001"},
		{"515", "66", "204043610000001", "515661037000001"},
		{"204", "04", "204043610000001", "515661037000001"},
	} {
		profile := ResolveCarrierProfile(SIMIdentity{
			HomeMCC: test.homeMCC,
			HomeMNC: test.homeMNC,
			ICCID:   "89636626000000000001",
			IMSI:    test.imsi,
		})
		if profile.ID != "ipcc-dito-51566" {
			t.Fatalf("carrier profile for %s%s = %q, want ipcc-dito-51566", test.homeMCC, test.homeMNC, profile.ID)
		}
		if got := profile.EffectiveSubscriberIMSI(test.imsi); got != test.want {
			t.Fatalf("rewritten subscriber IMSI for %s%s = %q, want %q", test.homeMCC, test.homeMNC, got, test.want)
		}
		if profile.IKEProposal != IKEProposalLegacy {
			t.Fatalf("IKE proposal for %s%s = %q", test.homeMCC, test.homeMNC, profile.IKEProposal)
		}
	}
	profile := ResolveCarrierProfile(SIMIdentity{HomeMCC: "515", HomeMNC: "66", SPN: "DITO"})
	for _, native := range []string{"515661015000001", "515661037000001"} {
		if got := profile.EffectiveSubscriberIMSI(native); got != native {
			t.Fatalf("native DITO subscriber IMSI changed to %q", got)
		}
	}
}

func TestBuiltinDITOProfileMatchesBroadICCIDAndMNCVariants(t *testing.T) {
	for _, homePLMN := range []struct{ mcc, mnc string }{
		{mcc: "204", mnc: "04"},
		{mcc: "204", mnc: "047"},
	} {
		profile := ResolveCarrierProfile(SIMIdentity{
			HomeMCC: homePLMN.mcc,
			HomeMNC: homePLMN.mnc,
			ICCID:   "89636600000000000001",
			IMSI:    "204047616000001",
		})
		if profile.ID != "ipcc-dito-51566" {
			t.Fatalf("DITO profile for %s%s = %q", homePLMN.mcc, homePLMN.mnc, profile.ID)
		}
		if got := profile.EffectiveSubscriberIMSI("204047616000001"); got != "515661015000001" {
			t.Fatalf("DITO rewrite for %s%s = %q", homePLMN.mcc, homePLMN.mnc, got)
		}
	}
}

func TestResolveCarrierProfilePrefersConstrainedMVNO(t *testing.T) {
	// Cricket MVNO on AT&T network
	cricket := ResolveCarrierProfile(SIMIdentity{
		ICCID: "8901150000000000001", IMSI: "310150000000001",
		HomeMCC: "310", HomeMNC: "150",
	})
	if !strings.Contains(cricket.ID, "cricket") {
		t.Fatalf("Cricket MVNO profile = %#v", cricket)
	}

	// Pure Talk MVNO on AT&T network via GID1
	pureTalk := ResolveCarrierProfile(SIMIdentity{
		IMSI: "310410000000001", HomeMCC: "310", HomeMNC: "410", GID1: "62FFFF",
	})
	if !strings.Contains(pureTalk.ID, "pure-talk") {
		t.Fatalf("Pure Talk MVNO profile = %#v", pureTalk)
	}
}

func TestResolveCarrierProfileUsesAppleGID1Selector(t *testing.T) {
	profile := ResolveCarrierProfile(SIMIdentity{
		IMSI: "234100000000001", HomeMCC: "234", HomeMNC: "10", GID1: "508FFFFF",
	})
	if !strings.Contains(profile.ID, "giffgaff") || profile.MatchSource != "hplmn+gid1" {
		t.Fatalf("giffgaff profile = %#v", profile)
	}
}

func TestResolveCarrierProfileGiffgaffIMSHeaders(t *testing.T) {
	profile := ResolveCarrierProfile(SIMIdentity{
		IMSI: "234100000000001", HomeMCC: "234", HomeMNC: "10", GID1: "508FFFFF",
	})
	options := profile.IMSRegisterOptions
	if profile.IMSTransport != "tcp" || options.ContactFormat != IMSContactFormatGSMA {
		t.Fatalf("giffgaff IMS transport/contact profile = %#v", profile)
	}
	if profile.IMSUserAgent != "iOS/18.6.2 iPhone" {
		t.Fatalf("giffgaff User-Agent = %q", profile.IMSUserAgent)
	}
	if options.SupportedHeader != nil || options.AllowHeader != nil {
		t.Fatalf("giffgaff REGISTER header overrides = supported=%v allow=%v", options.SupportedHeader, options.AllowHeader)
	}
	if options.PAccessNetworkInfo != nil {
		t.Fatalf("giffgaff unexpectedly defines a carrier PANI override = %v", *options.PAccessNetworkInfo)
	}
	if profile.PANIEnabled == nil || !*profile.PANIEnabled || profile.PANICountry != "AUTO" {
		t.Fatalf("giffgaff PANI behavior = enabled=%v country=%q", profile.PANIEnabled, profile.PANICountry)
	}
	if len(options.ContactExtraTags) != 2 || options.ContactExtraTags[0] != "+g.3gpp.mid-call" || options.ContactExtraTags[1] != "+g.3gpp.smsip" {
		t.Fatalf("giffgaff Contact tags = %#v", options.ContactExtraTags)
	}
}

// TestResolveCarrierProfileUltraMobileIMS locks the live-validated ePDG and
// REGISTER Contact capabilities to the Ultra Mobile carrier selector.
func TestResolveCarrierProfileUltraMobileIMS(t *testing.T) {
	profile := ResolveCarrierProfile(SIMIdentity{
		IMSI: "310240000000001", HomeMCC: "310", HomeMNC: "240", GID1: "4153FFFF",
	})
	if profile.ID != "ipcc-ultramint-mobile-310026" || profile.MatchSource != "hplmn+gid1" {
		t.Fatalf("Ultra Mobile profile = %#v", profile)
	}
	if profile.EPDG != "epdg.epc.mnc240.mcc310.pub.3gppnetwork.org" {
		t.Fatalf("Ultra Mobile ePDG = %q", profile.EPDG)
	}
	if profile.RouteMCC != "310" || profile.RouteMNC != "240" {
		t.Fatalf("Ultra Mobile route = %s/%s, want 310/240", profile.RouteMCC, profile.RouteMNC)
	}
	if profile.IMSIPSecEncryption != "aes-cbc" {
		t.Fatalf("Ultra Mobile IMS encryption = %q", profile.IMSIPSecEncryption)
	}
	wantTags := []string{
		`+g.3gpp.accesstype="wlan1"`,
		"+g.3gpp.smsip-msisdnless",
		"+g.3gpp.smsip-msisdn-less",
	}
	if got := profile.IMSRegisterOptions.ContactExtraTags; !slices.Equal(got, wantTags) {
		t.Fatalf("Ultra Mobile Contact tags = %#v, want %#v", got, wantTags)
	}
}

func TestResolveCarrierProfileATT(t *testing.T) {
	profile := ResolveCarrierProfile(SIMIdentity{
		ICCID: "8901410000000000001", IMSI: "310410000000001", HomeMCC: "310", HomeMNC: "410",
	})
	if !strings.Contains(profile.ID, "att") {
		t.Fatalf("AT&T profile = %#v", profile)
	}
	if profile.IMSIdentityProfile != IMSProfileATT || profile.IMSRegisterProfile != IMSProfileATT {
		t.Fatalf("AT&T IMS profiles = %q/%q, want %q/%q", profile.IMSIdentityProfile, profile.IMSRegisterProfile, IMSProfileATT, IMSProfileATT)
	}
	if profile.IMSRegisterOptions.ContactFormat != IMSContactFormatATT {
		t.Fatalf("AT&T ContactFormat = %q, want %q", profile.IMSRegisterOptions.ContactFormat, IMSContactFormatATT)
	}
	if profile.IMSRegisterOptions.PVisitedNetworkID != "one.att.net" {
		t.Fatalf("AT&T PVisitedNetworkID = %q, want one.att.net", profile.IMSRegisterOptions.PVisitedNetworkID)
	}
}

func TestResolveCarrierProfileRedPocketOutranksBroadATTICCID(t *testing.T) {
	identity := SIMIdentity{
		ICCID: "8901410000000000001", IMSI: "310170000000001",
		HomeMCC: "310", HomeMNC: "170", SPN: "Red Pocket", GID1: "42FFFF",
	}
	profile := ResolveCarrierProfile(identity)
	if profile.ID != "ipcc-redpocket-310170" || profile.MatchSource != "hplmn+gid1" {
		t.Fatalf("RedPocket profile = %#v", profile)
	}
	if profile.IMSIdentityProfile != IMSProfileATT || profile.IMSRegisterProfile != IMSProfileATT {
		t.Fatalf("RedPocket IMS profiles = %q/%q, want %q/%q", profile.IMSIdentityProfile, profile.IMSRegisterProfile, IMSProfileATT, IMSProfileATT)
	}
	if !IsATT310280(identity) {
		t.Fatalf("IsATT310280(RedPocket) = false, want true")
	}
	if profile.IMSRegisterOptions.ContactFormat != IMSContactFormatATT {
		t.Fatalf("RedPocket ContactFormat = %q, want %q", profile.IMSRegisterOptions.ContactFormat, IMSContactFormatATT)
	}
	if profile.IMSRegisterOptions.PVisitedNetworkID != "one.att.net" {
		t.Fatalf("RedPocket PVisitedNetworkID = %q, want one.att.net", profile.IMSRegisterOptions.PVisitedNetworkID)
	}
}

func TestResolveCarrierProfileStandardHasNoRegisterOverrides(t *testing.T) {
	profile := ResolveCarrierProfile(SIMIdentity{HomeMCC: "999", HomeMNC: "99"})
	if profile.ID != CarrierProfileStandard {
		t.Fatalf("profile = %q", profile.ID)
	}
	if profile.IMSRegisterOptions.ExpirySeconds != 0 {
		t.Fatalf("standard expiry = %d", profile.IMSRegisterOptions.ExpirySeconds)
	}
	if profile.SMSPreauthenticate || profile.SMSPreferRegisteredIdentity {
		t.Fatal("standard profile unexpectedly enables O2 SMS behavior")
	}
	if profile.IMSRegisterOptions.ContactFormat != "" {
		t.Fatalf("standard contact format = %q", profile.IMSRegisterOptions.ContactFormat)
	}
	if profile.IMSRegisterOptions.SupportedHeader != nil {
		t.Fatalf("standard supported header = %v", *profile.IMSRegisterOptions.SupportedHeader)
	}
	if profile.PANIEnabled != nil || profile.PANICountry != "" {
		t.Fatalf("standard PANI behavior = enabled=%v country=%q", profile.PANIEnabled, profile.PANICountry)
	}
	if profile.AllowSMSWithoutContactConfirmation {
		t.Fatal("standard profile should require SMS contact confirmation")
	}
}

func TestMVNOParentNetworkRouting(t *testing.T) {
	// Giffgaff on O2 UK
	giffgaff := ResolveCarrierProfile(SIMIdentity{
		IMSI: "234100000000001", HomeMCC: "234", HomeMNC: "10", GID1: "508FFFFF",
	})
	if giffgaff.RouteMCC != "234" || giffgaff.RouteMNC != "10" {
		t.Fatalf("giffgaff Route PLMN = %s-%s, want 234-10", giffgaff.RouteMCC, giffgaff.RouteMNC)
	}

	// VOXI on Vodafone UK
	voxi := ResolveCarrierProfile(SIMIdentity{
		IMSI: "234150000000001", HomeMCC: "234", HomeMNC: "15", SPN: "VOXI",
	})
	if !strings.Contains(voxi.ID, "voxi") || voxi.RouteMCC != "234" || voxi.RouteMNC != "15" {
		t.Fatalf("VOXI profile = %#v", voxi)
	}

	// SMARTY on Three UK
	smarty := ResolveCarrierProfile(SIMIdentity{
		IMSI: "234200000000001", HomeMCC: "234", HomeMNC: "20", SPN: "SMARTY",
	})
	if !strings.Contains(smarty.ID, "smarty") || smarty.RouteMCC != "234" || smarty.RouteMNC != "20" {
		t.Fatalf("SMARTY profile = %#v", smarty)
	}
}

func TestGlobalRoamingProviderResolution(t *testing.T) {
	// Truphone / BetterRoaming global 90143
	truphone := ResolveCarrierProfile(SIMIdentity{
		IMSI: "901430000000001", HomeMCC: "901", HomeMNC: "43",
	})
	if (!strings.Contains(truphone.ID, "truphone") && !strings.Contains(truphone.ID, "1global")) || truphone.EPDG != "epdg.eps.truphone.net" {
		t.Fatalf("Truphone global profile = %#v", truphone)
	}

	// Jersey Telecom 23450 (eSIM Go / 1GLOBAL / RedteaGO host)
	jersey := ResolveCarrierProfile(SIMIdentity{
		IMSI: "234500000000001", HomeMCC: "234", HomeMNC: "50",
	})
	if !strings.Contains(jersey.ID, "jersey-telecom") || jersey.EPDG != "epdg.epc.mnc050.mcc234.pub.3gppnetwork.org" {
		t.Fatalf("Jersey Telecom profile = %#v", jersey)
	}
}

func TestCTExcelMVNOResolution(t *testing.T) {
	ctexcel := ResolveCarrierProfile(SIMIdentity{
		IMSI:    "234330000000001",
		ICCID:   "8944300000000000001",
		SPN:     "CTExcel",
		HomeMCC: "234",
		HomeMNC: "33",
	})
	if ctexcel.ID != "ipcc-ctexcel-23433" {
		t.Fatalf("CTExcel profile ID = %q, want ipcc-ctexcel-23433", ctexcel.ID)
	}
	if ctexcel.IMSDialURIScheme != "sip" || !ctexcel.IMSUserEqPhone {
		t.Fatalf("CTExcel dial URI scheme = %q, userEqPhone = %v", ctexcel.IMSDialURIScheme, ctexcel.IMSUserEqPhone)
	}
}

func TestCarrierSwapSeparation(t *testing.T) {
	// 1. Red Pocket on AT&T (310280)
	redPocket := ResolveCarrierProfile(SIMIdentity{
		IMSI: "310280000000001", HomeMCC: "310", HomeMNC: "280", SPN: "Red Pocket", GID1: "42",
	})
	if redPocket.IMSIdentityProfile != IMSProfileATT || redPocket.IMSRegisterProfile != IMSProfileATT {
		t.Fatalf("Red Pocket profile = %q/%q, want att/att", redPocket.IMSIdentityProfile, redPocket.IMSRegisterProfile)
	}

	// 2. Swapped to Vodafone UK (23415)
	vodafone := ResolveCarrierProfile(SIMIdentity{
		IMSI: "234150000000001", HomeMCC: "234", HomeMNC: "15",
	})
	if vodafone.IMSIdentityProfile != IMSProfileStandard || vodafone.IMSRegisterProfile != IMSProfileStandard {
		t.Fatalf("Vodafone UK profile = %q/%q, want standard/standard", vodafone.IMSIdentityProfile, vodafone.IMSRegisterProfile)
	}
	if vodafone.IMSRegisterOptions.ContactFormat != "" || vodafone.IMSRegisterOptions.PVisitedNetworkID != "" {
		t.Fatalf("Vodafone UK leaked AT&T register options: %#v", vodafone.IMSRegisterOptions)
	}

	// 3. Swapped to Cricket (on AT&T)
	cricket := ResolveCarrierProfile(SIMIdentity{
		ICCID: "8901150000000000001", IMSI: "310410000000001", HomeMCC: "310", HomeMNC: "410",
	})
	if cricket.IMSIdentityProfile != IMSProfileATT || cricket.IMSRegisterProfile != IMSProfileATT {
		t.Fatalf("Cricket profile = %q/%q, want att/att", cricket.IMSIdentityProfile, cricket.IMSRegisterProfile)
	}

	// 4. Swapped to Unknown / Standard SIM (99999)
	standard := ResolveCarrierProfile(SIMIdentity{HomeMCC: "999", HomeMNC: "99"})
	if standard.IMSIdentityProfile != IMSProfileStandard || standard.IMSRegisterProfile != IMSProfileStandard {
		t.Fatalf("Standard profile = %q/%q, want standard/standard", standard.IMSIdentityProfile, standard.IMSRegisterProfile)
	}
}

func TestThreeHKCarrierProfileAndPANI(t *testing.T) {
	identity := SIMIdentity{
		IMSI:    "454030000000001",
		ICCID:   "8985203000000000001",
		HomeMCC: "454",
		HomeMNC: "03",
	}
	profile := ResolveCarrierProfile(identity)
	if profile.ID != "ipcc-hutchison-hk-45403" {
		t.Fatalf("3HK profile ID = %q, want ipcc-hutchison-hk-45403", profile.ID)
	}
	if profile.EPDG != "wlan.three.com.hk" {
		t.Fatalf("3HK ePDG = %q, want wlan.three.com.hk", profile.EPDG)
	}
	if profile.IMSIPSecMode != "optional" {
		t.Fatalf("3HK IMSIPSecMode = %q, want optional", profile.IMSIPSecMode)
	}
	if profile.PANIEnabled == nil || !*profile.PANIEnabled || profile.PANICountry != "AUTO" {
		t.Fatalf("3HK PANI configuration = enabled=%v country=%q", profile.PANIEnabled, profile.PANICountry)
	}
}

func TestO2UKProfileUsesICCIDFallbackAndOmitsPANI(t *testing.T) {
	profile := ResolveCarrierProfile(SIMIdentity{
		IMSI:    "234109999999999",
		ICCID:   "8944110000000000001",
		HomeMCC: "234",
		HomeMNC: "10",
	})
	if profile.ID != "ipcc-o2-23410" {
		t.Fatalf("O2 profile ID = %q, want ipcc-o2-23410", profile.ID)
	}
	if profile.PANIEnabled == nil || *profile.PANIEnabled {
		t.Fatalf("O2 PANI configuration = %v, want disabled", profile.PANIEnabled)
	}
	if profile.SMSPreauthenticate {
		t.Fatal("O2 SMS unexpectedly enables cached preauthentication")
	}
	if profile.SMSPreferRegisteredIdentity || !profile.SMSPreferTelephoneIdentity {
		t.Fatal("O2 SMS identity policy is not telephone-associated")
	}
	if profile.SMSCenter != "+447802092035" {
		t.Fatalf("O2 SMSC fallback = %q, want +447802092035", profile.SMSCenter)
	}
	if profile.SMSTargetMode != "sip-phone" {
		t.Fatalf("O2 SMS target mode = %q, want sip-phone", profile.SMSTargetMode)
	}
	if profile.SMSRecipientMode != "uk-national" {
		t.Fatalf("O2 SMS recipient mode = %q, want uk-national", profile.SMSRecipientMode)
	}
	if profile.SMSHeaderMode != "minimal" || !profile.SMSUseContactVia ||
		profile.SMSPANI != "IEEE-802.11;country=GB;i-wlan-node-id=820000000001" {
		t.Fatalf("O2 SMS MESSAGE profile = %#v", profile)
	}
	if profile.IMSRegisterOptions.ExpirySeconds != 600000 ||
		profile.IMSRegisterOptions.SupportedHeader == nil ||
		*profile.IMSRegisterOptions.SupportedHeader != "path,sec-agree" {
		t.Fatalf("O2 REGISTER options = %#v", profile.IMSRegisterOptions)
	}
}
