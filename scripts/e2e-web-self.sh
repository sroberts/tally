#!/bin/bash
# End-to-end check of --web-cert self against iOS Safari, on macOS.
#
# Builds tally, runs a scratch copy (its own HOME, port 47899) on this
# machine's Tailscale address, checks the certificate with openssl, curl
# and Apple's trust evaluation, then boots a throwaway iOS simulator and
# shows that Safari refuses the pad until tally's CA is trusted and signs
# in with a QR link once it is. The scratch tally reads herdr's state but
# nothing here sends a key.
#
# Needs Tailscale with MagicDNS running, Xcode with an iOS runtime, and
# herdr's socket (HERDR_SOCKET_PATH, default ~/.config/herdr/herdr.sock).
# Screenshots land in $OUT (default a new temporary directory).
set -euo pipefail

port=47899
socket=${HERDR_SOCKET_PATH:-$HOME/.config/herdr/herdr.sock}
runtime=${RUNTIME:-$(xcrun simctl list runtimes | awk '/^iOS/ {id=$NF} END {print id}')}
out=${OUT:-$(mktemp -d -t tally-e2e)}
scratch=$(mktemp -d -t tally-e2e-home)
mkdir -p "$out"

tailscale=$(command -v tailscale || echo /Applications/Tailscale.app/Contents/MacOS/Tailscale)
status=$("$tailscale" status --json)
name=$(python3 -c 'import json,sys; print(json.load(sys.stdin)["Self"]["DNSName"].rstrip("."))' <<<"$status")
ip=$(python3 -c 'import json,sys; print(json.load(sys.stdin)["Self"]["TailscaleIPs"][0])' <<<"$status")
base="https://$name:$port"

pass=0
fail=0
check() { # check DESCRIPTION COMMAND...
	if "${@:2}" >/dev/null 2>&1; then
		echo "ok    $1"
		pass=$((pass + 1))
	else
		echo "FAIL  $1"
		fail=$((fail + 1))
	fi
}
not() { ! "$@"; }

serve_pid=""
sim=""
cleanup() {
	[ -n "$serve_pid" ] && kill "$serve_pid" 2>/dev/null || true
	[ -n "$sim" ] && xcrun simctl delete "$sim" 2>/dev/null || true
	rm -rf "$scratch"
}
trap cleanup EXIT

tally=$scratch/tally
go build -o "$tally" "$(dirname "$0")/.."
run() { HOME=$scratch HERDR_SOCKET_PATH=$socket "$tally" "$@"; }

echo "== scratch tally on $ip:$port"
if lsof -nP -iTCP:$port -sTCP:LISTEN >/dev/null; then
	echo "something already listens on :$port" >&2
	exit 1
fi
HOME=$scratch HERDR_SOCKET_PATH=$socket "$tally" serve --web --web-cert self --no-device \
	--web-listen "$ip:$port" >"$out/serve.log" 2>&1 &
serve_pid=$!
for _ in $(seq 60); do grep -q 'browser pad certificate' "$out/serve.log" && break; sleep 0.5; done
if ! kill -0 "$serve_pid" 2>/dev/null || ! grep -q 'browser pad certificate' "$out/serve.log"; then
	echo "FAIL  tally serve didn't start; see $out/serve.log" >&2
	exit 1
fi
self=$scratch/.config/tally/web/self/$name
check "serve issued a certificate" test -s "$self/server.pem"

echo "== certificate, from this machine"
echo | openssl s_client -connect "$ip:$port" -servername "$name" 2>/dev/null | openssl x509 -outform DER >"$scratch/served.der"
openssl x509 -in "$self/ca.pem" -outform DER -out "$scratch/ca.der"
openssl x509 -in "$self/ca.pem" -out "$scratch/ca.pem"
openssl x509 -in "$self/server.pem" -out "$scratch/server.pem"
check "the served certificate is the one on disk" cmp "$scratch/served.der" <(openssl x509 -in "$self/server.pem" -outform DER)
check "openssl verifies it for the name" openssl verify -CAfile "$scratch/ca.pem" -verify_hostname "$name" "$scratch/server.pem"
check "openssl refuses another name" not openssl verify -CAfile "$scratch/ca.pem" -verify_hostname example.com "$scratch/server.pem"
check "curl trusting the CA gets the sign-in page" curl -sf --cacert "$scratch/ca.pem" -o /dev/null "$base/login"
check "curl without the CA is refused" not curl -sf -o /dev/null "$base/login"
check "Apple's trust evaluation accepts it" \
	bash -c "security verify-cert -c '$scratch/served.der' -r '$scratch/ca.der' -p ssl -s '$name' -L -l 2>&1 | grep -q 'verification successful'"
check "Apple's trust evaluation refuses another name" \
	bash -c "security verify-cert -c '$scratch/served.der' -r '$scratch/ca.der' -p ssl -s example.com -L -l 2>&1 | grep -q 'mismatch'"
# Even the CA's own key can't make a certificate Apple accepts for signed mail.
openssl req -new -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -keyout "$scratch/mail.key" \
	-subj /CN=ceo -addext subjectAltName=email:ceo@example.com -addext extendedKeyUsage=emailProtection \
	-out "$scratch/mail.csr" 2>/dev/null
openssl x509 -req -in "$scratch/mail.csr" -CA "$self/ca.pem" -CAkey "$self/ca.pem" -days 1 -copy_extensions copy \
	-outform DER -out "$scratch/mail.der" 2>/dev/null
check "Apple refuses signed mail from the CA's key" \
	bash -c "test -s '$scratch/mail.der' && ! security verify-cert -c '$scratch/mail.der' -r '$scratch/ca.der' -p smime -e ceo@example.com -L -l 2>&1 | grep -q 'verification successful'"
check "tally doctor: web cert ok" bash -c "HOME='$scratch' HERDR_SOCKET_PATH='$socket' '$tally' doctor 2>&1 | grep -q '^ok    web cert'"
(cd "$scratch" && run web --ca >/dev/null)
check "tally web --ca exports the CA" cmp "$scratch/tally-ca.cer" "$scratch/ca.der"

echo "== iOS Safari ($runtime)"
sim=$(xcrun simctl create tally-e2e "iPhone 17 Pro" "$runtime")
xcrun simctl boot "$sim"
xcrun simctl bootstatus "$sim" >/dev/null
safari() { # safari URL SCREENSHOT
	xcrun simctl terminate "$sim" com.apple.mobilesafari 2>/dev/null || true
	xcrun simctl openurl "$sim" "$1"
	sleep 10
	xcrun simctl io "$sim" screenshot "$out/$2" >/dev/null 2>&1
}
# A fresh single-use sign-in link, as the QR code carries. It isn't printed.
link() { run web | awk '$1 == "link" {print $2}'; }
browsers() { run web --list | grep -c ' from ' || true; }

# Safari's first launch on a new simulator is slow; get it out of the way.
safari "$base/login" 0-warmup.png

safari "$(link)" 1-untrusted.png
check "Safari without the CA doesn't sign in" test "$(browsers)" = 0

xcrun simctl keychain "$sim" add-root-cert "$scratch/tally-ca.cer"
safari "$(link)" 2-signed-in.png
check "Safari with the CA signs in from the QR link" test "$(browsers)" = 1

echo
echo "$pass passed, $fail failed; screenshots and serve.log in $out"
[ "$fail" = 0 ]
