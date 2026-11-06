// Package metadata projects saved storefront responses into rebuildable catalog
// data. These fixed mappings are shared by crawling and local maintenance.
package metadata

import "strings"

const Version = 1
const MaxBody = 16 << 20
const MaxRows = 20000

// Decimal amounts are TEXT to preserve the exact JSON number, without rounding,
// currency assumptions or conversion. Original response bytes live in products.
type Field struct{ Column, Path, Kind string }
type Table struct {
	Name   string
	Fields []Field
}

func fields(spec string) []Field {
	var out []Field
	for _, s := range strings.Fields(spec) {
		p := strings.Split(s, ":")
		out = append(out, Field{p[0], p[1], p[2]})
	}
	return out
}

var Product = Table{"product_metadata", fields(`
image:image:text web_image:webImage:text thumbnail:thumbnail:text thumbnail_200:thumbnail200:text thumbnail_100:thumbnail100:text thumbnail_80:thumbnail80:text thumbnail_40:thumbnail40:text
price:price:decimal special_price:specialPrice:decimal lowest_digital_price:lowestDigitalPrice:decimal lowest_print_price:lowestPrintPrice:decimal
file_last_modified:fileLastModified:text date_available:dateAvailable:text date_created:dateCreated:text date_modified:dateModified:text
status:status:bool enabled:enabled:text sort_priority:sortPriority:int publisher_id:publisherId:int sku:sku:text edition:edition:text promo_html:promoText:text
content_rating:contentRating:int ai_status:aiStatus:int rating_text:rating:text rating_value:rating:decimalText
is_audio:isAudio:bool is_video:isVideo:bool is_pwyw:isPwyw:bool is_community_content:isCommunityContent:bool workflow_status:workflowStatus:text
name:description.name:text description_html:description.description:text description_url:description.url:text slug:description.slug:text short_description:description.shortDescription:text video_preview_url:description.videoPreviewUrl:text
publisher_name:publisher.name:text publisher_thumbnail:publisher.thumbnail:text publisher_resource_id:publisher.publisherId:int publisher_slug:publisher.slug:text
publisher_rating_average:publisher.reviewRatings.average:decimal publisher_rating_one:publisher.reviewRatings.countOne:int publisher_rating_two:publisher.reviewRatings.countTwo:int publisher_rating_three:publisher.reviewRatings.countThree:int publisher_rating_four:publisher.reviewRatings.countFour:int publisher_rating_five:publisher.reviewRatings.countFive:int
on_sale:onSale:bool ranking_name:ranking.humanName:text ranking_image_url:ranking.imageUrl:text review_count:reviewCount:int is_bundle:isBundle:bool isbn:isbn:text
scanned_pdf:scannedPDF:bool filesize:filesize:decimal watermarked:watermarked:bool publisher_preview_file:publisherPreviewFile:text preview_full_file:previewFullFile:text preview_custom_file:previewCustomFile:text preview_audio_file:previewAudioFile:text
page_count_text:pagecount:text page_count:pagecount:integerText disable_copy:disableCp:bool disable_print:disablePrint:bool tuckbox_enabled:tuckbox.enabled:bool
community_author_id:communityContentAuthorId:int community_author_alias:communityContentAuthorAlias:text world_id:worldId:int creator_level_id:creatorLevelId:int
rating_average:reviewRatings.average:decimal rating_one:reviewRatings.countOne:int rating_two:reviewRatings.countTwo:int rating_three:reviewRatings.countThree:int rating_four:reviewRatings.countFour:int rating_five:reviewRatings.countFive:int
integration:integration:text gift_certificate:giftCertificate:bool ai:ai:bool handmade:handmade:bool
`)}
var Creators = Table{"product_creators", fields(`role:role:text ordinal:ordinal:int name:name:text`)}
var Options = Table{"product_options", fields(`ordinal:ordinal:int product_attribute_id:productAttributeId:int option_id:optionId:int option_name:optionName:text option_value_id:optionValueId:int option_value_name:optionValueName:text type:type:text description:description:text price:price:decimal price_to_add:priceToAdd:decimal msrp:msrp:decimal enabled:enabled:bool tax_class_id:taxClassId:int pwyw_minimum:pwywMinimum:decimal pwyw_suggested:pwywSuggested:decimal addon_type:addOnInfo.type:text addon_name:addOnInfo.name:text addon_price:addOnInfo.price:decimal addon_price_to_add:addOnInfo.priceToAdd:decimal`)}
var Files = Table{"product_files", fields(`ordinal:ordinal:int file_index:index:int filename:filename:text size_bytes:size:int size_mb_text:sizeMB:text`)}
var Taxonomy = Table{"product_taxonomy", fields(`kind:kind:text ordinal:ordinal:int taxonomy_id:taxonomyId:int image:image:text parent_id:parentId:int sort_order:sortOrder:int group_id:groupId:int active:active:bool show_main:showMain:bool show_main_sites:showMainSites:bool`)}
var Labels = Table{"product_taxonomy_labels", fields(`kind:kind:text taxonomy_ordinal:taxonomyOrdinal:int ordinal:ordinal:int name:name:text header_html:header:text language_id:languageId:int language_name:languageName:text language_code:languageCode:text language_iso_code:languageISOCode:text slug:slug:text`)}
var Ancestors = Table{"product_taxonomy_ancestors", fields(`kind:kind:text taxonomy_ordinal:taxonomyOrdinal:int ordinal:ordinal:int ancestor_id:ancestorId:int`)}
var Texts = Table{"product_metadata_texts", fields(`kind:kind:text ordinal:ordinal:int value:value:text`)}
var Children = []Table{Creators, Options, Files, Taxonomy, Labels, Ancestors, Texts}

func SQLType(f Field) string {
	if f.Kind == "int" || f.Kind == "bool" || f.Kind == "integerText" {
		return "INTEGER"
	}
	return "TEXT"
}
