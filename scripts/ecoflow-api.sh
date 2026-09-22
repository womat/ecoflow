#!/usr/bin/env bash
#
# ecoflow-api.sh - client for the EcoFlow cloud, reading except where it says.
#
# Signs requests the way the EcoFlow IoT Open API expects and prints the raw
# JSON response.
#
# Almost everything here reads. The exceptions are worth naming rather than
# glossing over, because "it cannot change the device" is a promise and it is
# no longer true of the whole script:
#
#   - "values" and "login" use POST. Both are still reads; POST is simply what
#     those endpoints want. The PUT endpoint that sets values is not used.
#   - "request" publishes to a .../get topic, which is a read request.
#   - "fast" publishes to a .../set topic, which is the one path here that can
#     change the device. It sends one captured message and nothing else, and
#     only when asked for by name - see the note in usage().
#
# Credentials come from the environment, never from flags or from this repo:
#
#   ECOFLOW_ACCESS_KEY   access key from https://developer-eu.ecoflow.com
#   ECOFLOW_SECRET_KEY   matching secret key
#   ECOFLOW_HOST         API host, default https://api-e.ecoflow.com (EU)
#                        use https://api-a.ecoflow.com for US accounts
#   ECOFLOW_PORTAL_TOKEN session token of the consumer web portal, for the
#                        "portal" commands only - see usage()
#   ECOFLOW_USER_ID      numeric account id, for "app-cert", "app-mqtt" and "live"
#
# Requires: bash, curl, openssl. jq only pretty-prints for devices, quota, get,
# cert, portal, portal-get and selftest; the other nine commands need it and
# stop without it. mqtt and app-mqtt also need mosquitto_sub; request, live and
# fast need mosquitto_pub as well.

set -euo pipefail

HOST="${ECOFLOW_HOST:-https://api-e.ecoflow.com}"
VERBOSE=0

# usage - the --help text; keep it in step with the case block in main()
usage() {
	cat <<'USAGE'
usage: ecoflow-api.sh [-v] <command> [args]

commands:
  devices              list the devices bound to this account
                       GET /iot-open/sign/device/list
  quota <SN>           read all values of one device
                       GET /iot-open/sign/device/quota/all?sn=<SN>
  get <path> [k=v ...] any other GET endpoint, parameters as key=value
  values <SN> <quota> [quota ...]
                       read named values of one device
                       POST /iot-open/sign/device/quota
                       e.g. bpSoc, bpPwr, mpptPwr, sysLoadPwr, sysGridPwr,
                       pcsAPhase, pcsBPhase, pcsCPhase, mpptHeartBeat
                       Needs jq.
  cert                 fetch the MQTT credentials for this account
                       GET /iot-open/sign/certification
  mqtt <SN> [suffix]   subscribe to the device's MQTT topic and print what
                       arrives, each line prefixed with a timestamp; suffix
                       defaults to "quota", other useful values are "status",
                       "get_reply" and "#" (everything, usually denied by the
                       ACL). Runs until Ctrl-C. Needs jq and mosquitto_sub.
  request <SN> <quota> [quota ...]
                       ask the device for named values over MQTT: listen on
                       .../get_reply and .../quota, publish the request to
                       .../get, print whatever arrives within ECOFLOW_WAIT
                       seconds (default 15). Listening on both matters because
                       the ACL may deny .../get_reply while granting .../quota.
                       This is the one command that publishes - see the note
                       below. Needs jq, mosquitto_sub and mosquitto_pub.
  login [email]        log in to the consumer portal and print the session token
                       on stdout, so it can be captured directly:
                         export ECOFLOW_PORTAL_TOKEN="$(ecoflow-api.sh login)"
                       The password is read from the terminal without echo, or
                       taken from ECOFLOW_PASSWORD. Needs jq. See the note below.
  portal <SN>          read the consumer portal's device detail: live power and
                       SoC, energy counters and the full quota blocks - what the
                       Developer API refuses for blocked models
                       GET /provider-service/user/device/detail?sn=<SN>
  status <SN>          the same data as one readable overview instead of the
                       raw JSON. Needs jq.
  portal-get <path>    any other GET against the portal API
  app-cert             fetch the MQTT credentials of the consumer app channel
                       GET /iot-auth/app/certification?userId=<ECOFLOW_USER_ID>
                       Uses the portal token, not the API keys. Needs jq.
  app-mqtt <SN> [suffix]
                       subscribe to one of the app's own topics under
                       /app/<userId>/<SN>/thing/property/; suffix defaults to
                       "set", where the phone app sends its commands - so
                       operating the app while this runs shows what it actually
                       sends. This command only ever subscribes; the one thing
                       here that publishes to "set" is "fast". Runs until
                       Ctrl-C. Needs jq and mosquitto_sub.
  live <SN>            watch the consumer app's MQTT channel and keep it awake:
                       subscribe to the device's push, reply and status topics
                       and publish a wake-up call every ECOFLOW_LIVE_INTERVAL
                       seconds, because the device stops pushing when nothing
                       asks. Payloads are printed as hex, with the text spelled
                       out when they are readable. Runs until Ctrl-C.
                       Needs jq, mosquitto_sub and mosquitto_pub.
  fast <SN>            like "live", but also switches on the device's fast
                       stream: roughly every three seconds instead of once a
                       minute. This is the one command that publishes to a
                       .../set topic - see the note below before using it.
                       Needs jq, mosquitto_sub and mosquitto_pub.
  selftest             check the signature assembly and the stream switch frame,
                       no keys and no network
  help                 show this message

environment:
  ECOFLOW_ACCESS_KEY   required (except for selftest and the portal commands)
  ECOFLOW_SECRET_KEY   required (except for selftest and the portal commands)
  ECOFLOW_HOST         default https://api-e.ecoflow.com
  ECOFLOW_EMAIL        account e-mail for "login", if not passed as an argument
  ECOFLOW_PASSWORD     account password for "login". Prefer the interactive
                       prompt: an exported password outlives the shell that set
                       it and ends up in process environments.
  ECOFLOW_PRODUCT_TYPE product family for the portal commands, default 85
                       (PowerOcean). It is the productKey the portal itself puts
                       in its URL; without a matching value the endpoint answers
                       with no data at all.
  ECOFLOW_USER_ID      numeric account id, required for "app-cert", "app-mqtt",
                       "live" and "fast".
                       "login" prints it ready to export; in the browser it is
                       the userId the portal sends with its own requests.
  ECOFLOW_LIVE_INTERVAL
                       seconds between the wake-up calls of "live", default 30.
                       Other integrations use 20 to 60; below that the device
                       gains nothing and the broker sees more traffic. Set it
                       to 0 to leave the wake-up calls out altogether and see
                       whether subscribing alone keeps the device talking -
                       every measurement so far was taken with them running.
  ECOFLOW_FAST_INTERVAL
                       seconds between the stream switches of "fast", default 3,
                       which is the rate the phone app uses. The fast stream
                       lapses if it is renewed much slower: at 10 seconds the
                       device fell back to one report a minute. Each switch
                       opens its own connection, so this is not free.
  ECOFLOW_PORTAL_TOKEN required for the portal commands. It is the session token
                       of https://user-portal.ecoflow.com, not an API key: open
                       the portal while logged in, then read the S1_JWT entry
                       under Local Storage in the browser's developer tools.
                       It expires - refetch it when a call returns 401. Keep it
                       out of shell history and out of this repository.

options:
  -v, --verbose        print the string that gets signed and the request
                       headers to stderr; the secret key is never printed

exit status:
  0  API answered with code 0
  1  usage or configuration error
  2  API answered with a non-zero code (1006 means the model is excluded)
  3  the request itself failed (network, TLS, ...)

examples:
  export ECOFLOW_ACCESS_KEY=... ECOFLOW_SECRET_KEY=...
  ecoflow-api.sh devices
  ecoflow-api.sh quota HC31XXXXXXXXXXXX
  ecoflow-api.sh -v get /iot-open/sign/device/list
  ecoflow-api.sh values HC31XXXXXXXXXXXX bpSoc bpPwr
  ecoflow-api.sh mqtt HC31XXXXXXXXXXXX
  ecoflow-api.sh mqtt HC31XXXXXXXXXXXX get_reply
  ecoflow-api.sh request HC31XXXXXXXXXXXX bpSoc bpPwr
  ECOFLOW_PORTAL_TOKEN=... ecoflow-api.sh portal HC31XXXXXXXXXXXX
  ECOFLOW_PORTAL_TOKEN=... ecoflow-api.sh status HC31XXXXXXXXXXXX
  ECOFLOW_USER_ID=... ecoflow-api.sh app-cert
  ECOFLOW_USER_ID=... ecoflow-api.sh live HC31XXXXXXXXXXXX
  ECOFLOW_USER_ID=... ecoflow-api.sh app-mqtt HC31XXXXXXXXXXXX set

note on "login":
  This is the consumer app's login endpoint, not a documented API. It takes the
  account password **base64-encoded, not hashed** - base64 is encoding, not
  encryption, so the password is protected by TLS alone. A portal session token
  read from the browser is the smaller secret and expires on its own; the login
  is the more convenient one. Neither is an official interface.

note:
  The "values" command uses POST because the API's read endpoint for named
  quotas is a POST. It is still a read: this script never touches the PUT
  endpoint that would set values on the device.

  Three commands publish; everything else only ever subscribes. "request" and
  "live" publish to a .../get topic, which is a read request, and the suffix is
  hard-wired so no other topic can be reached through them.

  "fast" is the exception and the only one that publishes to .../set. What it
  sends is not built from guesswork: the message was captured off the wire by
  subscribing to that topic while operating the phone app, and this script
  reproduces those bytes exactly, varying only the sequence number. It carries
  no parameters, and it is a separate command so that the write never happens
  as a side effect of asking for values - "live" stays free of it.

  Even so: .../set is the topic through which the device can be changed. An
  earlier attempt to assemble this message from third-party notes came out
  wrong in four places, which is why it was measured instead.

  "app-mqtt" subscribes to .../set by default, which is the opposite of writing
  to it: it shows what the phone app sends there.

  The MQTT password is passed to the mosquitto clients on the command line, so
  it is briefly visible to other users of this machine via the process list.
USAGE
}

# die MESSAGE [STATUS] - report and exit; STATUS defaults to 1
#
# Called inside a command substitution it only ends that subshell, so callers
# there have to propagate the status themselves.
die() {
	printf 'ecoflow-api.sh: %s\n' "$1" >&2
	exit "${2:-1}"
}

# sign_string ACCESS_KEY NONCE TIMESTAMP [k=v ...]
#
# The API expects the business parameters sorted by ASCII value and joined with
# "&", followed by accessKey, nonce and timestamp in exactly that order. Nested
# parameters would have to be flattened first; this script only passes flat
# key=value pairs, which is all the read endpoints need.
sign_string() {
	local access_key="$1" nonce="$2" timestamp="$3"
	shift 3

	local params=''
	if [ "$#" -gt 0 ]; then
		params="$(printf '%s\n' "$@" | LC_ALL=C sort | paste -sd'&' -)&"
	fi

	printf '%saccessKey=%s&nonce=%s&timestamp=%s' \
		"$params" "$access_key" "$nonce" "$timestamp"
}

# hmac_sha256 SECRET STRING -> lowercase hex
#
# Reading the last field works for both "(stdin)= abc..." from older openssl
# and "SHA2-256(stdin)= abc..." from newer versions.
hmac_sha256() {
	printf '%s' "$2" | openssl dgst -sha256 -hmac "$1" | awk '{print $NF}'
}

# api_get PATH [k=v ...] -> raw response body on stdout
api_get() {
	local path="$1"
	shift

	local access_key="${ECOFLOW_ACCESS_KEY:-}" secret_key="${ECOFLOW_SECRET_KEY:-}"
	[ -n "$access_key" ] || die 'ECOFLOW_ACCESS_KEY is not set'
	[ -n "$secret_key" ] || die 'ECOFLOW_SECRET_KEY is not set'

	# The API wants milliseconds; date +%s keeps this portable to BSD date.
	local timestamp nonce str sign query=''
	timestamp="$(( $(date +%s) * 1000 ))"
	nonce="$(( 100000 + RANDOM % 900000 ))"

	str="$(sign_string "$access_key" "$nonce" "$timestamp" "$@")"
	sign="$(hmac_sha256 "$secret_key" "$str")"

	# The reference client signs the raw query string and URL-encodes it only
	# afterwards, so signature and URL would disagree for values that need
	# escaping. Rather than get that subtly wrong, refuse such values: every
	# parameter these read endpoints take (serial numbers, quota names) is
	# plain ASCII anyway.
	local p
	for p in "$@"; do
		case "${p#*=}" in
		*[!A-Za-z0-9._~-]*) die "parameter value needs URL encoding, which this script does not do: $p" ;;
		esac
	done

	if [ "$#" -gt 0 ]; then
		query="?$(printf '%s\n' "$@" | LC_ALL=C sort | paste -sd'&' -)"
	fi

	if [ "$VERBOSE" -eq 1 ]; then
		{
			printf 'GET %s%s%s\n' "$HOST" "$path" "$query"
			printf 'signStr: %s\n' "$str"
			printf 'headers: accessKey=%s nonce=%s timestamp=%s sign=%s\n' \
				"$access_key" "$nonce" "$timestamp" "$sign"
		} >&2
	fi

	curl -sS "${HOST}${path}${query}" \
		-H "accessKey: $access_key" \
		-H "nonce: $nonce" \
		-H "timestamp: $timestamp" \
		-H "sign: $sign" || die 'request failed' 3
}

# flatten_json JSON -> one "key=value" per line, nested keys dotted and array
# elements indexed, e.g. params.quotas[0]=bpSoc. This is the shape the API
# expects for signing a request body.
flatten_json() {
	printf '%s' "$1" | jq -r '
		paths(scalars) as $p |
		($p | map(if type == "number" then "[" + tostring + "]" else "." + . end)
		    | join("") | ltrimstr(".")) + "=" + (getpath($p) | tostring)
	'
}

# api_post PATH JSON -> raw response body on stdout
#
# Only ever called with read endpoints; see the note in usage().
api_post() {
	local path="$1" body="$2"

	local access_key="${ECOFLOW_ACCESS_KEY:-}" secret_key="${ECOFLOW_SECRET_KEY:-}"
	[ -n "$access_key" ] || die 'ECOFLOW_ACCESS_KEY is not set'
	[ -n "$secret_key" ] || die 'ECOFLOW_SECRET_KEY is not set'

	local timestamp nonce str sign
	timestamp="$(( $(date +%s) * 1000 ))"
	nonce="$(( 100000 + RANDOM % 900000 ))"

	local -a flat=()
	while IFS= read -r line; do
		[ -n "$line" ] && flat+=("$line")
	done < <(flatten_json "$body" | LC_ALL=C sort)

	str="$(sign_string "$access_key" "$nonce" "$timestamp" ${flat[@]+"${flat[@]}"})"
	sign="$(hmac_sha256 "$secret_key" "$str")"

	if [ "$VERBOSE" -eq 1 ]; then
		{
			printf 'POST %s%s\n' "$HOST" "$path"
			printf 'body: %s\n' "$body"
			printf 'signStr: %s\n' "$str"
			printf 'headers: accessKey=%s nonce=%s timestamp=%s sign=%s\n' \
				"$access_key" "$nonce" "$timestamp" "$sign"
		} >&2
	fi

	curl -sS -X POST "${HOST}${path}" \
		-H 'Content-Type: application/json;charset=UTF-8' \
		-H "accessKey: $access_key" \
		-H "nonce: $nonce" \
		-H "timestamp: $timestamp" \
		-H "sign: $sign" \
		--data-binary "$body" || die 'request failed' 3
}

# response_code BODY -> the top-level result code, which is the first one in
# the response. Empty if the response carries no code at all.
response_code() {
	printf '%s' "$1" |
		grep -oE '"code"[[:space:]]*:[[:space:]]*"?[0-9]+' |
		head -1 | grep -oE '[0-9]+$' || true
}

# report_code CODE -> explain the code on stderr, return the script exit status
report_code() {
	local code="$1"

	case "$code" in
	0) return 0 ;;
	'')
		printf 'ecoflow-api.sh: no result code in response\n' >&2
		return 2
		;;
	1006)
		cat >&2 <<-'HINT'
			ecoflow-api.sh: code 1006 - this device model is not exposed through the
			Developer API. This is a model blocklist on EcoFlow's side, not a
			configuration problem; see api-status.md. Note that a blocked device can
			still show up in "devices" - the block hits the data call. Use local
			Modbus TCP instead.
		HINT
		return 2
		;;
	8512)
		cat >&2 <<-'HINT'
			ecoflow-api.sh: code 8512 - no permission for this serial number. It is
			most likely not bound to this account; check the SN against the output of
			"ecoflow-api.sh devices".
		HINT
		return 2
		;;
	*)
		printf 'ecoflow-api.sh: API returned code %s\n' "$code" >&2
		return 2
		;;
	esac
}

# request PATH [k=v ...] - fetch, print and judge one endpoint
#
# api_get runs in a command substitution, so its die() only ends that subshell;
# the status has to be propagated explicitly here.
request() {
	local body
	body="$(api_get "$@")" || exit $?
	show_and_judge "$body"
}

# portal_login [EMAIL] - print the portal session token on stdout
#
# Everything except the token goes to stderr, so the caller can capture it with
# a command substitution. The password is never echoed, never stored and never
# passed on a command line.
portal_login() {
	command -v jq >/dev/null 2>&1 || die 'login needs jq'

	local email="${1:-${ECOFLOW_EMAIL:-}}"
	if [ -z "$email" ]; then
		read -r -p 'EcoFlow account e-mail: ' email </dev/tty || die 'no e-mail given'
	fi
	[ -n "$email" ] || die 'no e-mail given'

	local password="${ECOFLOW_PASSWORD:-}"
	if [ -z "$password" ]; then
		read -rs -p 'Password (not echoed): ' password </dev/tty || die 'no password given'
		printf '\n' >&2
	fi
	[ -n "$password" ] || die 'no password given'

	# The endpoint wants the password base64-encoded. That is encoding, not
	# hashing - see the note in usage().
	local encoded
	encoded="$(printf '%s' "$password" | base64 | tr -d '\n')"
	password=''

	local payload
	payload="$(jq -cn --arg email "$email" --arg password "$encoded" \
		'{email: $email, password: $password, scene: "IOT_APP", userType: "ECOFLOW"}')"
	encoded=''

	# The payload carries the password, so it goes in over stdin: an argument
	# would be visible to anyone who can read the process list.
	local body
	body="$(printf '%s' "$payload" | curl -sS -X POST "${HOST}/auth/login" \
		-H 'Content-Type: application/json;charset=UTF-8' \
		--data-binary @-)" || die 'login request failed' 3
	payload=''

	local code
	code="$(response_code "$body")"
	if [ "$code" != '0' ]; then
		printf 'ecoflow-api.sh: login failed: %s\n' \
			"$(printf '%s' "$body" | jq -r '.message // "unknown error"')" >&2
		exit 2
	fi

	local token user_id
	token="$(printf '%s' "$body" | jq -r '.data.token // empty')"
	user_id="$(printf '%s' "$body" | jq -r '.data.user.userId // empty')"
	[ -n "$token" ] || die 'no token in the login response'

	# The user id goes to stderr like everything else, because stdout carries the
	# token and nothing else - but "live" needs it, so print it ready to paste.
	printf 'logged in as user %s\n' "$user_id" >&2
	[ -n "$user_id" ] &&
		printf 'for the "live" command: export ECOFLOW_USER_ID=%s\n' "$user_id" >&2

	printf '%s\n' "$token"
}

# portal_fetch URL AUTHORIZATION [HEADER ...] -> body, then the HTTP status on
# the last line
#
# The portal sends a product-type header alongside the token, and the endpoint
# answers with an empty body without it - code 0, no data, which looks like an
# empty account rather than a missing header. The browser also sends signature
# headers (x-appid, x-nonce, x-sign, x-timestamp); leaving them out changes
# nothing about the answer, so they are not sent here.
#
# Further headers can be appended: the app's certification endpoint lives on the
# same host and takes the same token, but expects a language header as well.
portal_fetch() {
	local url="$1" auth="$2"
	shift 2

	local -a extra=()
	local h
	for h in "$@"; do
		extra+=(-H "$h")
	done

	curl -sS -w '\n%{http_code}' "$url" \
		-H "Authorization: $auth" \
		-H "product-type: ${ECOFLOW_PRODUCT_TYPE:-85}" \
		${extra[@]+"${extra[@]}"}
}

# portal_get PATH [QUERY] [HEADER ...] -> raw response body on stdout
#
# The consumer web portal authenticates with a session token rather than the
# signed API keys, and its endpoints answer for devices the Developer API blocks.
# Whether the token is sent bare or with a "Bearer " prefix differs between
# deployments, so try it as given and retry prefixed on 401/403.
portal_get() {
	local path="$1" query="${2:-}"
	# Drop path and query, leaving any extra headers in "$@".
	shift $(($# > 2 ? 2 : $#))
	local token="${ECOFLOW_PORTAL_TOKEN:-}"
	[ -n "$token" ] || die 'ECOFLOW_PORTAL_TOKEN is not set (see --help)'

	local url="${HOST}${path}${query}"
	local body status

	body="$(portal_fetch "$url" "$token" "$@")" || die 'request failed' 3
	status="${body##*$'\n'}"
	body="${body%$'\n'*}"

	if [ "$status" = '401' ] || [ "$status" = '403' ]; then
		case "$token" in
		Bearer\ *) ;;
		*)
			[ "$VERBOSE" -eq 1 ] && printf 'retrying with a Bearer prefix\n' >&2
			body="$(portal_fetch "$url" "Bearer $token" "$@")" || die 'request failed' 3
			status="${body##*$'\n'}"
			body="${body%$'\n'*}"
			;;
		esac
	fi

	if [ "$VERBOSE" -eq 1 ]; then
		printf 'GET %s -> HTTP %s\n' "$url" "$status" >&2
	fi

	case "$status" in
	401 | 403)
		printf 'ecoflow-api.sh: HTTP %s - the portal token is missing, wrong or expired\n' \
			"$status" >&2
		printf '%s\n' "$body"
		exit 2
		;;
	esac

	printf '%s' "$body"
}

# portal_status SN - render the portal detail as a short overview
#
# The portal reports load and battery power negative while its own dashboard
# shows them positive, so the magnitude is printed and the direction spelled out
# rather than passing a sign through that the reader would have to interpret.
#
# The directions are measured, not assumed, because the signs are not uniform:
# bpPwr is positive while charging, but sysGridPwr is positive while exporting -
# the opposite of what EcoFlow's own field documentation suggests for actPwr.
# Settled by an energy balance on a sunny reading: 3378 W of PV split into 514 W
# house, 609 W into the battery and 2255 W to the grid, which only adds up if
# the positive grid figure leaves the house.
portal_status() {
	command -v jq >/dev/null 2>&1 || die 'status needs jq'

	local body
	body="$(portal_get /provider-service/user/device/detail "?sn=$1")" || exit $?

	local code
	code="$(response_code "$body")"
	if [ "$code" != '0' ]; then
		show_and_judge "$body"
		return
	fi

	printf '%s' "$body" | jq -e '.data' >/dev/null 2>&1 ||
		die 'the response carried no data - wrong ECOFLOW_PRODUCT_TYPE for this device?' 2

	printf '%s' "$body" | jq -r '
		.data as $d
		| (($d.quota // {}) | to_entries
		   | map(select(.key | endswith("ENERGY_STREAM_REPORT")))
		   | first | .value) as $stream
		| def w(x): (x // 0) | round | tostring;
		  [
		    "device   : \($d.systemName // "?") (\(if $d.online == 1 then "online" else "offline" end))",
		    "SoC      : \($d.bpSoc // "?") %",
		    "PV       : \(w($d.mpptPwr)) W",
		    "grid     : \(w(($d.sysGridPwr // 0) | fabs)) W\(if ($d.sysGridPwr // 0) > 0 then " (export)" elif ($d.sysGridPwr // 0) < 0 then " (import)" else " (idle)" end)",
		    "house    : \(w(($d.sysLoadPwr // 0) | fabs)) W",
		    "battery  : \(w(($d.bpPwr // 0) | fabs)) W\(if ($d.bpPwr // 0) < 0 then " (discharging)" elif ($d.bpPwr // 0) > 0 then " (charging)" else " (idle)" end)",
		    "",
		    "yield    : today \($d.todayElectricityGeneration // "?") | month \($d.monthElectricityGeneration // "?") | year \($d.yearElectricityGeneration // "?") | total \($d.totalElectricityGeneration // "?") kWh",
		    "measured : \(if $stream.timestamp then ($stream.timestamp | todate) else "unknown" end)"
		  ] | .[]'
}

# show_and_judge BODY - pretty-print a response and turn its code into a status
show_and_judge() {
	if command -v jq >/dev/null 2>&1; then
		printf '%s' "$1" | jq . || printf '%s\n' "$1"
	else
		printf '%s\n' "$1"
	fi

	report_code "$(response_code "$1")"
}

# read_values SN QUOTA... - POST /iot-open/sign/device/quota
read_values() {
	command -v jq >/dev/null 2>&1 || die 'values needs jq'

	local sn="$1"
	shift

	local body
	body="$(jq -cn --arg sn "$sn" '$ARGS.positional as $q | {sn: $sn, params: {quotas: $q}}' \
		--args "$@")"

	local response
	response="$(api_post /iot-open/sign/device/quota "$body")" || exit $?
	show_and_judge "$response"
}

# mqtt_credentials - fetch the MQTT credentials and set MQTT_ACCOUNT,
# MQTT_PASSWORD, MQTT_URL, MQTT_PORT plus the MQTT_TLS array
mqtt_credentials() {
	local body code
	body="$(api_get /iot-open/sign/certification)" || exit $?
	code="$(response_code "$body")"
	if [ "$code" != '0' ]; then
		printf '%s' "$body" | jq . >&2 || printf '%s\n' "$body" >&2
		report_code "$code"
		exit $?
	fi

	MQTT_ACCOUNT="$(printf '%s' "$body" | jq -r '.data.certificateAccount')"
	MQTT_PASSWORD="$(printf '%s' "$body" | jq -r '.data.certificatePassword')"
	MQTT_URL="$(printf '%s' "$body" | jq -r '.data.url')"
	MQTT_PORT="$(printf '%s' "$body" | jq -r '.data.port')"

	[ -n "$MQTT_ACCOUNT" ] && [ "$MQTT_ACCOUNT" != 'null' ] ||
		die 'no MQTT credentials in response'

	mqtt_tls_opts
}

# mqtt_tls_opts - fill the MQTT_TLS array with the right trust store options
#
# Newer mosquitto knows the OS store, older builds need an explicit file or
# directory. Both MQTT channels need this, so it lives on its own.
mqtt_tls_opts() {
	MQTT_TLS=()
	if mosquitto_sub --help 2>&1 | grep -q -- '--tls-use-os-certs'; then
		MQTT_TLS=(--tls-use-os-certs)
	elif [ -f /etc/ssl/cert.pem ]; then
		MQTT_TLS=(--cafile /etc/ssl/cert.pem)
	elif [ -d /etc/ssl/certs ]; then
		MQTT_TLS=(--capath /etc/ssl/certs)
	else
		die 'no CA trust store found for TLS'
	fi
}

# app_credentials - fetch the MQTT credentials of the consumer app channel
#
# Same globals as mqtt_credentials, but a different door: this endpoint
# authenticates with the portal session token instead of the signed API keys,
# and it answers for devices the Developer API blocks with 1006. Unlike the
# Open API's channel, data is actually published on this one.
#
# The portal's own variant of this endpoint
# (/iot-auth/enterprise-development/user/certification) returns the same fields
# AES-encrypted and needs no userId; it is described in api-status.md but not
# implemented here, because this one answers in plain JSON.
app_credentials() {
	local user_id="${ECOFLOW_USER_ID:-}"
	[ -n "$user_id" ] || die 'ECOFLOW_USER_ID is not set - "login" prints it (see --help)'
	case "$user_id" in
	*[!0-9]*) die "user id must be numeric: $user_id" ;;
	esac

	local body code
	body="$(portal_get /iot-auth/app/certification "?userId=${user_id}" 'lang: en_US')" || exit $?
	code="$(response_code "$body")"
	if [ "$code" != '0' ]; then
		printf '%s' "$body" | jq . >&2 || printf '%s\n' "$body" >&2
		report_code "$code"
		exit $?
	fi

	MQTT_ACCOUNT="$(printf '%s' "$body" | jq -r '.data.certificateAccount // empty')"
	MQTT_PASSWORD="$(printf '%s' "$body" | jq -r '.data.certificatePassword // empty')"
	MQTT_URL="$(printf '%s' "$body" | jq -r '.data.url // empty')"
	MQTT_PORT="$(printf '%s' "$body" | jq -r '.data.port // empty')"

	[ -n "$MQTT_ACCOUNT" ] || die 'no MQTT credentials in response'

	mqtt_tls_opts
}

# mqtt_subscribe SN [SUFFIX] - subscribe to the device topic, read-only
mqtt_subscribe() {
	local sn="$1" suffix="${2:-quota}"

	command -v jq >/dev/null 2>&1 || die 'mqtt needs jq'
	command -v mosquitto_sub >/dev/null 2>&1 ||
		die 'mqtt needs mosquitto_sub (brew install mosquitto, apt install mosquitto-clients)'

	mqtt_credentials
	local topic="/open/${MQTT_ACCOUNT}/${sn}/${suffix}"

	# In verbose mode let mosquitto_sub report the protocol handshake, so a
	# granted SUBACK can be told apart from a topic that is simply silent.
	local -a debug=()
	if [ "$VERBOSE" -eq 1 ]; then
		debug=(-d)
		printf 'mosquitto_sub -h %s -p %s -u %s -P <password> %s -d -t %s\n' \
			"$MQTT_URL" "$MQTT_PORT" "$MQTT_ACCOUNT" "${MQTT_TLS[*]}" "$topic" >&2
	fi

	# Timestamp every message: a quiet log is only evidence if you can tell when
	# it was quiet.
	printf 'subscribing to %s (Ctrl-C to stop)\n' "$topic" >&2
	mosquitto_sub -h "$MQTT_URL" -p "$MQTT_PORT" -u "$MQTT_ACCOUNT" -P "$MQTT_PASSWORD" \
		"${MQTT_TLS[@]}" ${debug[@]+"${debug[@]}"} -t "$topic" -F '%I %t %p'
}

# mqtt_request SN QUOTA... - ask the device over MQTT and wait for the reply
#
# This publishes, which nothing else in this script does. The topic suffix is
# hard-wired to "get": the "set" topic, which would change the device, cannot be
# reached through this command by construction.
mqtt_request() {
	command -v jq >/dev/null 2>&1 || die 'request needs jq'
	command -v mosquitto_sub >/dev/null 2>&1 || die 'request needs mosquitto_sub'
	command -v mosquitto_pub >/dev/null 2>&1 || die 'request needs mosquitto_pub'

	local sn="$1"
	shift
	local wait="${ECOFLOW_WAIT:-15}"

	mqtt_credentials
	local reply_topic="/open/${MQTT_ACCOUNT}/${sn}/get_reply"
	local quota_topic="/open/${MQTT_ACCOUNT}/${sn}/quota"
	local get_topic="/open/${MQTT_ACCOUNT}/${sn}/get"

	local payload
	payload="$(jq -cn --arg id "$(date +%s)" \
		'$ARGS.positional as $q | {id: $id, version: "1.0", params: {quotas: $q}}' \
		--args "$@")"

	if [ "$VERBOSE" -eq 1 ]; then
		{
			printf 'subscribe: %s\n' "$reply_topic"
			printf 'subscribe: %s\n' "$quota_topic"
			printf 'publish:   %s\n' "$get_topic"
			printf 'payload:   %s\n' "$payload"
		} >&2
	fi

	# Listen on the reply topic and on quota: the ACL denies .../get_reply on at
	# least one account (SUBACK 0x80) while granting .../quota, and a denied
	# subscription does not stop mosquitto_sub as long as another one is granted.
	printf 'listening on %s and %s for %ss\n' "$reply_topic" "$quota_topic" "$wait" >&2
	mosquitto_sub -h "$MQTT_URL" -p "$MQTT_PORT" -u "$MQTT_ACCOUNT" -P "$MQTT_PASSWORD" \
		"${MQTT_TLS[@]}" -t "$reply_topic" -t "$quota_topic" -F '%I %t %p' -W "$wait" &
	local sub_pid=$!

	# Give the subscription a moment to be established before asking.
	sleep 2

	# Publish over MQTT v5 at QoS 1 and show the handshake: only v5 carries a
	# reason code in the PUBACK, which is what distinguishes "the broker refused
	# to forward this" (0x87, not authorized) from "the device ignored it".
	# Fall back to the protocol default if the broker does not speak v5.
	printf 'publishing request to %s\n' "$get_topic" >&2
	if ! mosquitto_pub -h "$MQTT_URL" -p "$MQTT_PORT" -u "$MQTT_ACCOUNT" -P "$MQTT_PASSWORD" \
		"${MQTT_TLS[@]}" -t "$get_topic" -m "$payload" -V 5 -q 1 -d; then
		printf 'MQTT v5 publish failed, retrying with the protocol default\n' >&2
		mosquitto_pub -h "$MQTT_URL" -p "$MQTT_PORT" -u "$MQTT_ACCOUNT" -P "$MQTT_PASSWORD" \
			"${MQTT_TLS[@]}" -t "$get_topic" -m "$payload" -q 1 -d ||
			die 'publishing the request failed' 3
	fi

	wait "$sub_pid" || true
	printf 'done waiting; no output above means the device did not answer\n' >&2
}

# hex_lines - pass frames through, adding a decoded line when they are text
#
# The payload arrives as hex: that is lossless, and it keeps binary protobuf
# frames from tearing up the terminal. Some topics answer in JSON though, so a
# payload that is entirely printable gets a second, indented line with the text.
# Raw first, reading second - the same rule modbusread follows for its words.
hex_lines() {
	awk '
	BEGIN {
		for (i = 0; i < 16; i++) {
			d = sprintf("%x", i)
			H[d] = i
			H[toupper(d)] = i
		}
	}
	{
		print
		fflush()
		if (NF < 4) next
		hex = $NF
		if (length(hex) == 0 || length(hex) % 2 != 0) next
		text = ""
		ok = 1
		for (i = 1; i < length(hex); i += 2) {
			c = H[substr(hex, i, 1)] * 16 + H[substr(hex, i + 1, 1)]
			if (c < 32 || c > 126) {
				ok = 0
				break
			}
			text = text sprintf("%c", c)
		}
		if (ok) {
			print "    text: " text
			fflush()
		}
	}'
}

# mqtt_live SN - watch the consumer app's MQTT channel and keep it awake
#
# This is the answer to a measured problem: the portal REST endpoint hands out
# the last state the device pushed into the cloud, not a fresh reading. With no
# client asking, the device stops pushing and "status" keeps reporting the same
# measured timestamp for hours. Several third-party integrations work around
# this by publishing a wake-up call every 20 to 60 seconds; that is what the
# loop below does.
#
# This publishes, which only "request" did before, and like there the topic is
# hard-wired to .../get - a read request. The .../set topic, which the app uses
# to switch on its fast stream, stays unreachable from this script.
#
# The payloads are unverified transcriptions from other projects' reverse
# engineering, so the output stays deliberately raw: this is a measuring tool
# first. See api-status.md for what is established and what is not.
mqtt_live() {
	local sn="$1" mode="${2:-}"

	command -v jq >/dev/null 2>&1 || die 'live needs jq'
	command -v mosquitto_sub >/dev/null 2>&1 ||
		die 'live needs mosquitto_sub (brew install mosquitto, apt install mosquitto-clients)'
	command -v mosquitto_pub >/dev/null 2>&1 || die 'live needs mosquitto_pub'

	local interval="${ECOFLOW_LIVE_INTERVAL:-30}"
	case "$interval" in
	'' | *[!0-9]*) die "ECOFLOW_LIVE_INTERVAL must be a number of seconds: $interval" ;;
	esac

	app_credentials
	local user_id="$ECOFLOW_USER_ID"

	local push_topic="/app/device/property/${sn}"
	local reply_topic="/app/${user_id}/${sn}/thing/property/get_reply"
	local state_topic="/app/device/status/${sn}"
	local get_topic="/app/${user_id}/${sn}/thing/property/get"
	local set_topic="/app/${user_id}/${sn}/thing/property/set"

	local -a debug=()
	local quiet=/dev/null
	if [ "$VERBOSE" -eq 1 ]; then
		debug=(-d)
		# Let the wake-up calls report their own handshake instead of being
		# silenced: whether they reach the broker is the whole question here.
		quiet=/dev/stderr
		{
			printf 'subscribe: %s\n' "$push_topic"
			printf 'subscribe: %s\n' "$reply_topic"
			printf 'subscribe: %s\n' "$state_topic"
			if [ "$interval" -gt 0 ]; then
				printf 'publish:   %s every %ss\n' "$get_topic" "$interval"
				printf 'payload:   %s\n' "$(wake_payload)"
			else
				printf 'publish:   nothing - wake-up calls disabled\n'
			fi
			printf 'broker:    %s:%s as %s with <password>\n' \
				"$MQTT_URL" "$MQTT_PORT" "$MQTT_ACCOUNT"
		} >&2
	fi

	# The wake-up calls run in the background while the subscription holds the
	# foreground. The first one waits for the subscription to be established,
	# the same two-step "request" uses.
	#
	# Every connection gets a fresh client id: the broker rejects ids that do
	# not look like the app's own, and it refuses to reuse one it has already
	# seen. Hence a random one per publish rather than a single shared id.
	#
	# An interval of 0 leaves them out entirely. That is not a convenience: it
	# is the only way to answer whether the wake-up call does anything at all,
	# since every measurement so far was taken with it running. Subscribing
	# alone may well be what keeps the device talking.
	local pub_pid=''
	if [ "$interval" -gt 0 ]; then
		(
			sleep 3
			while :; do
				mosquitto_pub -h "$MQTT_URL" -p "$MQTT_PORT" \
					-u "$MQTT_ACCOUNT" -P "$MQTT_PASSWORD" "${MQTT_TLS[@]}" \
					${debug[@]+"${debug[@]}"} \
					-i "$(app_client_id "$user_id")" \
					-t "$get_topic" -m "$(wake_payload)" >"$quiet" 2>&1 ||
					printf 'wake-up call failed, retrying in %ss\n' "$interval" >&2
				sleep "$interval"
			done
		) &
		pub_pid=$!
	else
		printf 'wake-up calls disabled - subscription only\n' >&2
	fi

	# The fast stream, and the only place in this script that publishes to a
	# .../set topic. It sends exactly one message - the EnergyStreamSwitch as it
	# was captured off the wire, nothing parameterised and nothing assembled
	# from someone's notes - and only when "fast" was typed on the command line.
	local switch_pid=''
	if [ "$mode" = 'fast' ]; then
		(
			sleep 2
			local seq=1
			while :; do
				# MQTT v5 at QoS 1 on purpose: only there does the PUBACK carry
			# a reason code, which separates "the broker refused to forward
			# this" from "the device ignored it". The same measurement the
			# "request" command relies on, and the only way to tell whether a
			# stream that stays slow is a rejected message or an ineffective
			# one.
			stream_switch_frame "$sn" "$seq" |
					mosquitto_pub -h "$MQTT_URL" -p "$MQTT_PORT" \
						-u "$MQTT_ACCOUNT" -P "$MQTT_PASSWORD" "${MQTT_TLS[@]}" \
						${debug[@]+"${debug[@]}"} \
						-i "$(app_client_id "$user_id")" \
						-t "$set_topic" -s -V 5 -q 1 >"$quiet" 2>&1 ||
					printf 'stream switch was not accepted\n' >&2
				# The app repeats this every few seconds and the stream stops
				# again when nothing renews it. The counter wraps at 127 so the
				# sequence stays a single-byte varint, as the app's own does.
				seq=$((seq % 127 + 1))
				sleep "${ECOFLOW_FAST_INTERVAL:-3}"
			done
		) &
		switch_pid=$!
	fi

	# The only trap in this file, and it earns its place: everything else either
	# runs in the foreground or ends itself on a timeout, but this loop would
	# outlive the Ctrl-C that stops the subscription. Its sleeping child is
	# taken down as well, so nothing is left running in the background.
	#
	# Ctrl-C is the way to stop this: the terminal signals the whole process
	# group, so the subscription ends at once and this trap runs right after. A
	# signal sent to this process alone waits for the subscription to finish
	# first, because bash defers traps until the foreground command returns.
	# Unquoted on purpose: either may be empty when that loop was not started,
	# and an empty argument would make kill complain. Process ids never split.
	trap 'for p in $pub_pid $switch_pid; do
		pkill -P "$p" 2>/dev/null || true
		kill "$p" 2>/dev/null || true
	done' INT TERM EXIT

	printf 'subscribing to %s, %s and %s (Ctrl-C to stop)\n' \
		"$push_topic" "$reply_topic" "$state_topic" >&2
	mosquitto_sub -h "$MQTT_URL" -p "$MQTT_PORT" -u "$MQTT_ACCOUNT" -P "$MQTT_PASSWORD" \
		"${MQTT_TLS[@]}" ${debug[@]+"${debug[@]}"} -i "$(app_client_id "$user_id")" \
		-t "$push_topic" -t "$reply_topic" -t "$state_topic" \
		-F '%I %t %l %x' | hex_lines
}

# app_watch SN [SUFFIX] - subscribe to one of the app's own device topics
#
# The point is to watch what the app does rather than guess it. The interesting
# one is "set": that is where the app publishes the commands this script will
# not send, so subscribing to it while operating the app on the phone captures
# the real bytes. Hence the default.
#
# Subscribing only - this adds no write path.
app_watch() {
	local sn="$1" suffix="${2:-set}"

	command -v jq >/dev/null 2>&1 || die 'app-mqtt needs jq'
	command -v mosquitto_sub >/dev/null 2>&1 ||
		die 'app-mqtt needs mosquitto_sub (brew install mosquitto, apt install mosquitto-clients)'

	app_credentials
	local topic="/app/${ECOFLOW_USER_ID}/${sn}/thing/property/${suffix}"

	local -a debug=()
	if [ "$VERBOSE" -eq 1 ]; then
		debug=(-d)
		printf 'mosquitto_sub -h %s -p %s -u %s -P <password> -t %s\n' \
			"$MQTT_URL" "$MQTT_PORT" "$MQTT_ACCOUNT" "$topic" >&2
	fi

	printf 'subscribing to %s (Ctrl-C to stop)\n' "$topic" >&2
	mosquitto_sub -h "$MQTT_URL" -p "$MQTT_PORT" -u "$MQTT_ACCOUNT" -P "$MQTT_PASSWORD" \
		"${MQTT_TLS[@]}" ${debug[@]+"${debug[@]}"} -i "$(app_client_id "$ECOFLOW_USER_ID")" \
		-t "$topic" -F '%I %t %l %x' | hex_lines
}

# wake_payload - the request that keeps the device talking
#
# Transcribed from other projects' captures, not from EcoFlow documentation. A
# fresh id per call, because that is what the app sends and a repeated one may
# well be discarded as a duplicate.
wake_payload() {
	jq -cn --arg id "$(date +%s)000" \
		'{from: "Android", id: $id, moduleType: 0,
		  operateType: "latestQuotas", params: {}, version: "1.0"}'
}

# hex_to_bytes HEX - write the raw bytes of a hex string to stdout
#
# Pure bash on purpose: xxd is not everywhere, and this runs a handful of times.
hex_to_bytes() {
	local hex="$1" i
	for ((i = 0; i < ${#hex}; i += 2)); do
		printf '\x'"${hex:i:2}"
	done
}

# stream_switch_frame SN SEQ - the message that turns on the fast stream
#
# Not derived from anyone's notes: captured off the wire on 22 September 2026 by
# subscribing to .../set while operating the phone app (see "app-mqtt"). The
# app repeats it every few seconds, and the device answers with an energy
# stream every three seconds instead of every minute.
#
# Byte for byte, as measured:
#
#   field 1  pdata      08 01 10 01   two flags, both 1 - plain, not obfuscated
#   field 2  src        32            the app
#   field 3  dest       96            the energy management unit
#   field 4  dSrc       1
#   field 5  dDest      1
#   field 7  (unknown)  3
#   field 8  cmd_func   96
#   field 9  cmd_id     97
#   field 10 dataLen    4
#   field 11 needAck    1
#   field 14 seq        small counter, 1 upwards
#   field 16 version    3
#   field 17 payloadVer 1
#   field 23 from       "ios" - what the captured app called itself
#   field 25 sn         the serial number as ASCII
#
# Only the sequence number and the serial number vary, so the rest is a
# constant. Guarding the two lengths keeps them single-byte varints, which is
# what makes that possible.
stream_switch_frame() {
	local sn="$1" seq="$2"

	[ "$seq" -ge 1 ] && [ "$seq" -le 127 ] || die "sequence out of range: $seq"

	# -v matters: without it od replaces a 16-byte line that repeats the one
	# before with a single "*", which would silently shorten the frame on the
	# one topic that can change the device.
	local sn_hex
	sn_hex="$(printf '%s' "$sn" | od -v -An -tx1 | tr -d ' \n')"
	local sn_len=$((${#sn_hex} / 2))
	[ "$sn_len" -ge 1 ] && [ "$sn_len" -le 60 ] || die "serial number has an odd length: $sn"

	local inner=''
	inner+='0a0408011001' # 1  pdata = 08 01 10 01
	inner+='1020'         # 2  src        32
	inner+='1860'         # 3  dest       96
	inner+='2001'         # 4  dSrc        1
	inner+='2801'         # 5  dDest       1
	inner+='3803'         # 7  unknown     3
	inner+='4060'         # 8  cmd_func   96
	inner+='4861'         # 9  cmd_id     97
	inner+='5004'         # 10 dataLen     4
	inner+='5801'         # 11 needAck     1
	inner+="70$(printf '%02x' "$seq")" # 14 seq
	inner+='800103'       # 16 version     3
	inner+='880101'       # 17 payloadVer  1
	inner+='ba0103696f73' # 23 from     "ios"
	inner+="ca01$(printf '%02x' "$sn_len")${sn_hex}" # 25 sn

	hex_to_bytes "$(printf '0a%02x%s' "$((${#inner} / 2))" "$inner")"
}

# app_client_id USERID - a client id the app's broker accepts
#
# Observed by others: ids that do not carry the app's prefix and the account's
# user id are refused, and an id that has already connected is refused again
# after a disconnect. So build a fresh random one each time. openssl is a
# requirement of this script anyway; uuidgen would be a new one.
app_client_id() {
	printf 'ANDROID_%s_%s' "$(openssl rand -hex 16 | tr 'a-f' 'A-F')" "$1"
}

# Check the signature implementation against the official test vector from
# EcoFlow's documentation (developer-eu.ecoflow.com, "HTTP access steps"). This
# is not self-generated: matching it means the assembly order, the flattening of
# nested bodies and the HMAC all agree with the published spec.
selftest() {
	local failed=0 got want

	# Vector 1: signed request body from the documentation.
	local ak='Fp4SvIprYSDPXtYJidEtUAd1o'
	local sk='WIbFEKre0s6sLnh4ei7SPUeYnptHG6V'
	local body='{"sn":"123456789","params":{"cmdSet":11,"id":24,"eps":0}}'

	if command -v jq >/dev/null 2>&1; then
		got="$(flatten_json "$body" | LC_ALL=C sort | paste -sd'&' -)"
		want='params.cmdSet=11&params.eps=0&params.id=24&sn=123456789'
		[ "$got" = "$want" ] || {
			printf 'selftest: flattened body is %s, want %s\n' "$got" "$want" >&2
			failed=1
		}

		# Vector 2: the documented flattening example, which also covers arrays
		# of scalars and arrays of objects.
		got="$(flatten_json '{"name":"demo1","ids":[1,2,3],"deviceInfo":{"id":1},"deviceList":[{"id":1},{"id":2}]}' |
			LC_ALL=C sort | paste -sd'&' -)"
		want='deviceInfo.id=1&deviceList[0].id=1&deviceList[1].id=2&ids[0]=1&ids[1]=2&ids[2]=3&name=demo1'
		[ "$got" = "$want" ] || {
			printf 'selftest: flattened example is %s, want %s\n' "$got" "$want" >&2
			failed=1
		}
	else
		printf 'selftest: jq missing, skipping the body flattening checks\n' >&2
	fi

	got="$(sign_string "$ak" '345164' '1671171709428' \
		'params.cmdSet=11' 'params.eps=0' 'params.id=24' 'sn=123456789')"
	want='params.cmdSet=11&params.eps=0&params.id=24&sn=123456789&accessKey=Fp4SvIprYSDPXtYJidEtUAd1o&nonce=345164&timestamp=1671171709428'
	[ "$got" = "$want" ] || {
		printf 'selftest: signStr is %s\n' "$got" >&2
		failed=1
	}

	got="$(hmac_sha256 "$sk" "$want")"
	want='07c13b65e037faf3b153d51613638fa80003c4c38d2407379a7f52851af1473e'
	[ "$got" = "$want" ] || {
		printf 'selftest: sign is %s, want %s\n' "$got" "$want" >&2
		failed=1
	}

	# Vector 3: the stream switch frame, against the bytes the Go
	# implementation builds from the same captured original. The serial here
	# repeats its first sixteen bytes on purpose - that is exactly what od
	# collapses to a "*" unless it is called with -v, which once shortened this
	# frame by thirteen bytes on the one topic that can change the device.
	got="$(stream_switch_frame 'AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA' 7 | od -v -An -tx1 | tr -d ' \n')"
	want='0a490a04080110011020186020012801380340604861500458017007800103880101ba0103696f73ca01204141414141414141414141414141414141414141414141414141414141414141'
	[ "$got" = "$want" ] || {
		printf 'selftest: stream switch frame is %s, want %s\n' "$got" "$want" >&2
		failed=1
	}

	[ "$failed" -eq 0 ] || die 'selftest FAILED'
	printf 'selftest OK (signature matches the official test vector)\n'
}

# main - parse the global options, then dispatch on the command
main() {
	while [ "$#" -gt 0 ]; do
		case "$1" in
		-v | --verbose)
			VERBOSE=1
			shift
			;;
		-h | --help | help)
			usage
			exit 0
			;;
		--)
			shift
			break
			;;
		-*) die "unknown option: $1" ;;
		*) break ;;
		esac
	done

	[ "$#" -gt 0 ] || {
		usage >&2
		exit 1
	}

	local cmd="$1" body
	shift

	case "$cmd" in
	devices)
		[ "$#" -eq 0 ] || die 'devices takes no arguments'
		request /iot-open/sign/device/list
		;;
	quota)
		[ "$#" -eq 1 ] || die 'usage: ecoflow-api.sh quota <SN>'
		request /iot-open/sign/device/quota/all "sn=$1"
		;;
	get)
		[ "$#" -ge 1 ] || die 'usage: ecoflow-api.sh get <path> [k=v ...]'
		local path="$1"
		shift
		case "$path" in
		/*) ;;
		*) die 'path must start with /' ;;
		esac
		local p
		for p in "$@"; do
			case "$p" in
			*=*) ;;
			*) die "parameter must be key=value: $p" ;;
			esac
		done
		request "$path" "$@"
		;;
	values)
		[ "$#" -ge 2 ] || die 'usage: ecoflow-api.sh values <SN> <quota> [quota ...]'
		read_values "$@"
		;;
	cert)
		[ "$#" -eq 0 ] || die 'cert takes no arguments'
		request /iot-open/sign/certification
		;;
	mqtt)
		[ "$#" -ge 1 ] && [ "$#" -le 2 ] || die 'usage: ecoflow-api.sh mqtt <SN> [suffix]'
		mqtt_subscribe "$@"
		;;
	request)
		[ "$#" -ge 2 ] || die 'usage: ecoflow-api.sh request <SN> <quota> [quota ...]'
		mqtt_request "$@"
		;;
	login)
		[ "$#" -le 1 ] || die 'usage: ecoflow-api.sh login [email]'
		portal_login "$@"
		;;
	portal)
		[ "$#" -eq 1 ] || die 'usage: ecoflow-api.sh portal <SN>'
		case "$1" in
		*[!A-Za-z0-9_-]*) die "serial number looks wrong: $1" ;;
		esac
		body="$(portal_get /provider-service/user/device/detail "?sn=$1")" || exit $?
		show_and_judge "$body"
		;;
	status)
		[ "$#" -eq 1 ] || die 'usage: ecoflow-api.sh status <SN>'
		case "$1" in
		*[!A-Za-z0-9_-]*) die "serial number looks wrong: $1" ;;
		esac
		portal_status "$1"
		;;
	portal-get)
		[ "$#" -eq 1 ] || die 'usage: ecoflow-api.sh portal-get <path>'
		case "$1" in
		/*) ;;
		*) die 'path must start with /' ;;
		esac
		body="$(portal_get "$1")" || exit $?
		show_and_judge "$body"
		;;
	app-mqtt)
		[ "$#" -ge 1 ] && [ "$#" -le 2 ] || die 'usage: ecoflow-api.sh app-mqtt <SN> [suffix]'
		case "$1" in
		*[!A-Za-z0-9_-]*) die "serial number looks wrong: $1" ;;
		esac
		app_watch "$@"
		;;
	app-cert)
		[ "$#" -eq 0 ] || die 'app-cert takes no arguments'
		command -v jq >/dev/null 2>&1 || die 'app-cert needs jq'
		[ -n "${ECOFLOW_USER_ID:-}" ] ||
			die 'ECOFLOW_USER_ID is not set - "login" prints it (see --help)'
		body="$(portal_get /iot-auth/app/certification \
			"?userId=${ECOFLOW_USER_ID}" 'lang: en_US')" || exit $?
		show_and_judge "$body"
		;;
	live)
		[ "$#" -eq 1 ] || die 'usage: ecoflow-api.sh live <SN>'
		case "$1" in
		*[!A-Za-z0-9_-]*) die "serial number looks wrong: $1" ;;
		esac
		mqtt_live "$1"
		;;
	fast)
		[ "$#" -eq 1 ] || die 'usage: ecoflow-api.sh fast <SN>'
		case "$1" in
		*[!A-Za-z0-9_-]*) die "serial number looks wrong: $1" ;;
		esac
		mqtt_live "$1" fast
		;;
	selftest)
		selftest
		;;
	*)
		die "unknown command: $cmd"
		;;
	esac
}

main "$@"
