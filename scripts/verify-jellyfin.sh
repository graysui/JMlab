#!/usr/bin/env bash
set -euo pipefail

# Run inside the deployment host or against a forwarded local port. This is a
# MediaBrowser wire-contract regression for Swiftfin. It never creates a new
# work: the selected item must already have been generated from a ranking or
# restored through the administrator migration flow.
BASE_URL="${JMLAB_BASE_URL:-http://127.0.0.1:8097}"
USERNAME="${JMLAB_USERNAME:-admin}"
PASSWORD="${JMLAB_PASSWORD:-}"
if [[ -z "$PASSWORD" ]]; then
  echo "JMLAB_PASSWORD 必须设置" >&2
  exit 2
fi

json_header=(-H 'Content-Type: application/json')
info=$(curl -fsS "$BASE_URL/System/Info")
jq -e '.ProductName == "JMlab" and .Id == "jmlab" and .ServerName == "JMlab"' <<<"$info" >/dev/null
public_info=$(curl -fsS "$BASE_URL/System/Info/Public")
jq -e '.ProductName == "JMlab" and .Id == "jmlab"' <<<"$public_info" >/dev/null

login=$(curl -fsS "${json_header[@]}" \
  -H 'X-Emby-Authorization: MediaBrowser Client="Swiftfin Verify", Device="Contract", DeviceId="swiftfin-verify", Version="1.0"' \
  -d "{\"Username\":\"$USERNAME\",\"Pw\":\"$PASSWORD\"}" \
  "$BASE_URL/Users/AuthenticateByName")
token=$(jq -er '.AccessToken' <<<"$login")
user_id=$(jq -er '.User.Id' <<<"$login")
emby=(
  -H "X-Emby-Token: $token"
  -H 'X-Emby-Client: Swiftfin Verify'
  -H 'X-Emby-Device-Name: Swiftfin Contract'
  -H 'X-Emby-Device-Id: swiftfin-verify'
  -H 'X-Emby-Client-Version: 1.0'
)
bearer=(-H "Authorization: Bearer $token" -H 'Content-Type: application/json')

users=$(curl -fsS "${emby[@]}" "$BASE_URL/Users")
jq -e --arg id "$user_id" 'any(.[]; .Id == $id)' <<<"$users" >/dev/null
user=$(curl -fsS "${emby[@]}" "$BASE_URL/Users/$user_id")
jq -e --arg id "$user_id" '.Id == $id' <<<"$user" >/dev/null
views=$(curl -fsS "${emby[@]}" "$BASE_URL/Users/$user_id/Views")
library_id="${JMLAB_LIBRARY_ID:-$(jq -er '.Items[0].Id // empty' <<<"$views")}"

# Swiftfin sends both a user-scoped Items request and a root Items request.
visible_items=$(curl -fsS "${emby[@]}" "$BASE_URL/Users/$user_id/Items?ParentId=$library_id&IncludeItemTypes=Movie&Limit=1")
item_id="${JMLAB_ITEM_ID:-}"
if [[ -z "$item_id" ]]; then
  item_id=$(jq -er '.Items[0].Id // empty' <<<"$visible_items") || {
    echo "媒体库没有可回归的作品；请先通过模块添加内容并创建媒体库，或设置 JMLAB_ITEM_ID" >&2
    exit 3
  }
fi
item=$(curl -fsS "${emby[@]}" "$BASE_URL/Users/$user_id/Items/$item_id")
jq -e --arg id "$item_id" '.Id == $id and .Type == "Movie"' <<<"$item" >/dev/null
root_items=$(curl -fsS "${bearer[@]}" "$BASE_URL/Items?ParentId=$library_id&IncludeItemTypes=Movie&Limit=1")
jq -e --arg id "$item_id" 'any(.Items[]; .Id == $id and .Type == "Movie")' <<<"$root_items" >/dev/null
legacy_items=$(curl -fsS -G --data-urlencode "api_key=$token" --data-urlencode "ParentId=$library_id" --data-urlencode 'IncludeItemTypes=Movie' --data-urlencode 'Limit=1' "$BASE_URL/Items")
jq -e --arg id "$item_id" 'any(.Items[]; .Id == $id)' <<<"$legacy_items" >/dev/null

code=$(jq -er '.Name' <<<"$item")
encoded_code=$(jq -rn --arg value "$code" '$value | @uri')
hints=$(curl -fsS "${emby[@]}" "$BASE_URL/Search/Hints?SearchTerm=$encoded_code")
jq -e --arg id "$item_id" 'any(.SearchHints[]; .ItemId == $id and .Type == "Movie")' <<<"$hints" >/dev/null

foreign_status=$(curl -sS -o /dev/null -w '%{http_code}' "${emby[@]}" "$BASE_URL/Users/not-this-user/Views")
test "$foreign_status" = 403

state=$(curl -fsS "${emby[@]}" "$BASE_URL/Users/$user_id/Items/$item_id/UserData")
jq -e --arg id "$item_id" '.ItemId == $id and .Key == $id' <<<"$state" >/dev/null
initial_favorite=$(jq -r '.IsFavorite' <<<"$state")
initial_played=$(jq -r '.Played' <<<"$state")
initial_position=$(jq -r '.PlaybackPositionTicks' <<<"$state")
updated=$(curl -fsS "${bearer[@]}" -X POST \
  -d '{"IsFavorite":true,"Played":true,"PlaybackPositionTicks":10000000}' \
  "$BASE_URL/Users/$user_id/Items/$item_id/UserData")
jq -e '.IsFavorite and .Played and .PlaybackPositionTicks == 10000000' <<<"$updated" >/dev/null

play_session="swiftfin-verify-$(date +%s)"
play_event='{"ItemId":"'"$item_id"'","PositionTicks":10000000,"RunTimeTicks":200000000,"PlaySessionId":"'"$play_session"'","DeviceId":"swiftfin-verify","DeviceName":"Swiftfin Contract","Client":"Swiftfin Verify","IsPaused":false}'
for endpoint in Playing Playing/Progress Playing/Stopped; do
  status=$(curl -sS -o /dev/null -w '%{http_code}' "${bearer[@]}" -X POST -d "$play_event" "$BASE_URL/Sessions/$endpoint")
  test "$status" = 204
done
played_state=$(curl -fsS "${emby[@]}" "$BASE_URL/Users/$user_id/Items/$item_id/UserData")
jq -e '.PlaybackPositionTicks == 10000000 and .Played' <<<"$played_state" >/dev/null

favorite_status=$(curl -sS -o /dev/null -w '%{http_code}' "${emby[@]}" -X POST "$BASE_URL/Users/$user_id/FavoriteItems/$item_id")
test "$favorite_status" = 200
played_status=$(curl -sS -o /dev/null -w '%{http_code}' "${emby[@]}" -X POST "$BASE_URL/Users/$user_id/PlayedItems/$item_id")
test "$played_status" = 200

suffix="$(date +%s)"
playlist=$(curl -fsS "${emby[@]}" -X POST "$BASE_URL/Playlists?Name=Swiftfin%20Verify%20$suffix&UserId=$user_id&MediaType=Video")
playlist_id=$(jq -er '.Id' <<<"$playlist")
add_status=$(curl -sS -o /dev/null -w '%{http_code}' "${emby[@]}" -X POST "$BASE_URL/Playlists/$playlist_id/Items?Ids=$item_id")
test "$add_status" = 204
playlist_items=$(curl -fsS "${emby[@]}" "$BASE_URL/Playlists/$playlist_id/Items?UserId=$user_id")
jq -e --arg id "$item_id" 'any(.Items[]; .Id == $id)' <<<"$playlist_items" >/dev/null

# Restore the selected item's basic state so a compatibility check does not
# change a user's favorite/watch marker. Playback and playlist audit records
# remain available for diagnosing the client contract.
restore_payload=$(jq -cn --argjson favorite "$initial_favorite" --argjson played "$initial_played" --argjson position "$initial_position" '{IsFavorite:$favorite,Played:$played,PlaybackPositionTicks:$position}')
curl -fsS "${bearer[@]}" -X POST -d "$restore_payload" "$BASE_URL/Users/$user_id/Items/$item_id/UserData" >/dev/null

curl -fsS "$BASE_URL/health/ready" >/dev/null
printf 'Swiftfin/Jellyfin contract OK: user=%s item=%s code=%s playlist=%s\n' "$user_id" "$item_id" "$code" "$playlist_id"
