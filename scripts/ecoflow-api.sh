#!/usr/bin/env bash
#
# ecoflow-api.sh - read-only client for the EcoFlow Developer/Open API.
#
# Signs requests the way the EcoFlow IoT Open API expects and prints the raw
# JSON response. Like modbusread, this tool is deliberately read-only: it only
# ever issues GET requests, so it cannot change anything on the device.
#
# Credentials come from the environment, never from flags or from this repo:
#
#   ECOFLOW_ACCESS_KEY   access key from https://developer-eu.ecoflow.com
#   ECOFLOW_SECRET_KEY   matching secret key
#   ECOFLOW_HOST         API host, default https://api-e.ecoflow.com (EU)
#                        use https://api-a.ecoflow.com for US accounts
#
# Requires: bash, curl, openssl. jq is used for pretty-printing when present and
# is mandatory for the mqtt and request commands; mqtt needs mosquitto_sub, request
# needs mosquitto_pub as well.

set -euo pipefail

HOST="${ECOFLOW_HOST:-https://api-e.ecoflow.com}"
VERBOSE=0

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
                       ask the device for named values over MQTT: subscribe to
                       .../get_reply, publish the request to .../get, print
                       whatever arrives within ECOFLOW_WAIT seconds (default 15).
                       This is the one command that publishes - see the note
                       below. Needs jq, mosquitto_sub and mosquitto_pub.
  selftest             check the signature assembly, no keys and no network
  help                 show this message

environment:
  ECOFLOW_ACCESS_KEY   required (except for selftest)
  ECOFLOW_SECRET_KEY   required (except for selftest)
  ECOFLOW_HOST         default https://api-e.ecoflow.com

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

note:
  The "values" command uses POST because the API's read endpoint for named
  quotas is a POST. It is still a read: this script never touches the PUT
  endpoint that would set values on the device.

  "request" is the only command that publishes, and it can only publish to the
  .../get topic: the suffix is hard-wired, there is no free topic argument, so
  the .../set topic that would change the device stays unreachable from here.
  Everything else only ever subscribes.

  The MQTT password is passed to the mosquitto clients on the command line, so
  it is briefly visible to other users of this machine via the process list.
USAGE
}

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

	str="$(sign_string "$access_key" "$nonce" "$timestamp" "${flat[@]}")"
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

	# TLS trust store: newer mosquitto knows the OS store, older builds need an
	# explicit file or directory.
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
		"${MQTT_TLS[@]}" "${debug[@]}" -t "$topic" -F '%I %t %p'
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
	local get_topic="/open/${MQTT_ACCOUNT}/${sn}/get"

	local payload
	payload="$(jq -cn --arg id "$(date +%s)" \
		'$ARGS.positional as $q | {id: $id, version: "1.0", params: {quotas: $q}}' \
		--args "$@")"

	if [ "$VERBOSE" -eq 1 ]; then
		{
			printf 'subscribe: %s\n' "$reply_topic"
			printf 'publish:   %s\n' "$get_topic"
			printf 'payload:   %s\n' "$payload"
		} >&2
	fi

	printf 'listening on %s for %ss\n' "$reply_topic" "$wait" >&2
	mosquitto_sub -h "$MQTT_URL" -p "$MQTT_PORT" -u "$MQTT_ACCOUNT" -P "$MQTT_PASSWORD" \
		"${MQTT_TLS[@]}" -t "$reply_topic" -F '%I %t %p' -W "$wait" &
	local sub_pid=$!

	# Give the subscription a moment to be established before asking.
	sleep 2

	printf 'publishing request to %s\n' "$get_topic" >&2
	mosquitto_pub -h "$MQTT_URL" -p "$MQTT_PORT" -u "$MQTT_ACCOUNT" -P "$MQTT_PASSWORD" \
		"${MQTT_TLS[@]}" -t "$get_topic" -m "$payload" ||
		die 'publishing the request failed' 3

	wait "$sub_pid" || true
	printf 'done waiting; no output above means the device did not answer\n' >&2
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

	[ "$failed" -eq 0 ] || die 'selftest FAILED'
	printf 'selftest OK (matches the official test vector)\n'
}

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

	local cmd="$1"
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
	selftest)
		selftest
		;;
	*)
		die "unknown command: $cmd"
		;;
	esac
}

main "$@"
