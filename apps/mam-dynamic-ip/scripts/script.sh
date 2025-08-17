#!/usr/bin/env bash

: "${GLUETUN_CONTROL_SERVER_HOST:=localhost}"
: "${GLUETUN_CONTROL_SERVER_PROTOCOL:=http}"
: "${GLUETUN_CONTROL_SERVER_PORT:=8080}"
: "${GLUETUN_ENABLED:=true}"
: "${MAM_SESSION_ID}"
: "${MAM_SESSION_DIR:=/config/mam.cookies}"
: "${MAM_DYNAMIC_IP_URL:=https://t.myanonamouse.net/json/dynamicSeedbox.php}"
: "${IP_CACHE_FILE:=/config/ip.txt}"
: "${UPDATE_INTERVAL:=false}"
: "${LOG_TIMESTAMP}"

gluetun_origin="${GLUETUN_CONTROL_SERVER_PROTOCOL}://${GLUETUN_CONTROL_SERVER_HOST}:${GLUETUN_CONTROL_SERVER_PORT}"

log() {
  gum_opts=("--structured")

  if [[ "${LOG_TIMESTAMP}" == "true" ]]; then
    gum_opts+=("--time" "rfc3339")
  fi

  gum log "${gum_opts[@]}" "$@"
}

query_gluetun_control_server() {
  local endpoint="$1"
  local curl_opts=()

  if [[ -n "${GLUETUN_CONTROL_SERVER_API_KEY}" ]]; then
    curl_opts+=("-H" "X-API-KEY: ${GLUETUN_CONTROL_SERVER_API_KEY}")
  fi

  curl -s "${curl_opts[@]}" "${gluetun_origin}${endpoint}"
}

get_gluetun_external_ip() {
  local output
  output=$(query_gluetun_control_server "/v1/publicip/ip")
  echo "${output}" | jq -r .'public_ip'
}

query_mam() {
  local endpoint="$1"
  local session_id="$2"
  local curl_opts=()

  if [[ -z "${MAM_SESSION_DIR}" ]]; then
    log --level error "MAM_SESSION_DIR to store cookies is required."
    exit 1
  fi

  curl_opts+=("-c" "${MAM_SESSION_DIR}")

  if [[ ! -z "${session_id}" ]]; then
    curl_opts+=("-b" "mam_id=${session_id}")
  else
    curl_opts+=("-b" "${MAM_SESSION_DIR}")
  fi

  curl -s "${curl_opts[@]}" "${endpoint}"
}

get_mam_session_cookie() {
  local output
  local session_id="$1"

  if [[ -z "${session_id}" ]]; then
    return
  fi

  if [[ -f "${MAM_SESSION_DIR}" && -s "${MAM_SESSION_DIR}" ]]; then
      log --level info "Session cookie file already exists, skipping session creation"
      return
  fi

  output=$(query_mam "${MAM_DYNAMIC_IP_URL}" "${session_id}")
  msg=$(echo "${output}" | jq -r '.msg')

  if [ "${msg}" != "Completed" ] && [ "${msg}" != "No change" ]; then
    log --level error "Something went wrong. Refer to https://myanonamouse.net/api/endpoint.php/3/json/dynamicSeedbox.php" msg "${msg}"
  fi
}

post_mam_dynamic_ip() {
  local output
  output=$(query_mam "${MAM_DYNAMIC_IP_URL}")
  echo "${output}" | jq -r '.msg'
}

should_update_ip() {
  current_ip="$1"
  current_time=$(date +%s)
  update_interval=$((60 * 60)) # Set the update interval to 1 hour (in seconds)

  local last_ip
  local last_update_time

  if [ -f "${IP_CACHE_FILE}" ]; then
    last_ip=$(head -n 1 "${IP_CACHE_FILE}")
    last_update_time=$(tail -n 1 "${IP_CACHE_FILE}")
  fi

  if [[ "${current_ip}" != "${last_ip}" ]]; then
    # Check if the update was done in the last hour
    if [[ "${UPDATE_INTERVAL}" == "yes" ]] && [ -n "${last_update_time}" ]; then
      time_elapsed=$((current_time - last_update_time))

      if [ $time_elapsed -lt $update_interval ]; then
        log --level info "skipping: update already done in the last hour" \
          "external_ip" "${current_ip}" \
          "last_ip" "${last_ip}"

        return 0
      fi
    fi

    log --level info "different IP detected" \
      "external_ip" "${current_ip}" \
      "last_ip" "${last_ip}"

    return 0
  fi

  log --level info "same IP detected" \
    "external_ip" "${current_ip}" \
    "last_ip" "${last_ip}"

  return 1
}

main() {
  log --level info "Starting check" "gluetun_url" "${gluetun_origin}"

  if [[ "${GLUETUN_ENABLED}" == "true" ]]; then
    external_ip=$(get_gluetun_external_ip)
  else
    external_ip=$(curl -s "https://ipinfo.io/ip")
  fi

  if [[ -z "${external_ip}" ]]; then
    log --level error "External IP is empty. Potential VPN or internet connection issue."
    exit 1
  fi

  log --level info "Fetched configuration" \
    "external_ip" "${external_ip}" \
    "MAM_session_file" "${MAM_SESSION_DIR}" "MAM_session_id" "${MAM_SESSION_ID}"

  # try to hit MAM at least once
  # returns if session_id is not passed or cookie file is already present
  get_mam_session_cookie "${MAM_SESSION_ID}"

  # continuously update MAM with IP if applicable
  # see should_update_ip()
  if should_update_ip "${external_ip}"; then
    log --level info "Updating IP address with MAM" "external_ip" "${external_ip}"
    msg=$(post_mam_dynamic_ip)

    if [ "${msg}" == "Completed" ] || [ "${msg}" == "No change" ]; then
      current_time=$(date +%s)
      log --level info "called MAM" "current_time" "${current_time}"

      # store cached external_ip for later checks
      printf "%s\n%s" "${external_ip}" "${current_time}" > "${IP_CACHE_FILE}"
    fi
  fi
}

# entrypoint

main
