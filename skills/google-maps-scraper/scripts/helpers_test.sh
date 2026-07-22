#!/usr/bin/env bash

set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
test_root=$(mktemp -d)
trap 'rm -rf "$test_root"' EXIT

fake_bin="$test_root/bin"
mkdir -p "$fake_bin"

cat > "$fake_bin/npx" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "$SKILL_UPDATE_LOG"
EOF
chmod +x "$fake_bin/npx"

export SKILL_UPDATE_LOG="$test_root/skill-update.log"

PATH="$fake_bin:$PATH" bash "$script_dir/ensure-latest.sh"

if [[ $(cat "$SKILL_UPDATE_LOG") != '--yes skills update google-maps-scraper --yes' ]]; then
	echo "ensure-latest.sh did not perform the expected noninteractive skill update" >&2
	exit 1
fi

cat > "$fake_bin/npx" <<'EOF'
#!/usr/bin/env bash
exit 1
EOF
chmod +x "$fake_bin/npx"

if ! PATH="$fake_bin:$PATH" bash "$script_dir/ensure-latest.sh" >/dev/null 2>&1; then
	echo "ensure-latest.sh blocked use when the update check failed" >&2
	exit 1
fi

export XDG_CONFIG_HOME="$test_root/config"
secret='do-not-print-this-proxy-secret'

configure_output=$(printf 'http://user:%s@proxy.example:8080\n\n' "$secret" | bash "$script_dir/configure-proxy.sh" 2>&1)
proxy_file="$XDG_CONFIG_HOME/google-maps-scraper/proxies.txt"

if [[ ! -f "$proxy_file" ]]; then
	echo "configure-proxy.sh did not create the proxy file" >&2
	exit 1
fi

if [[ $(stat -c '%a' "$proxy_file" 2>/dev/null || stat -f '%Lp' "$proxy_file") != '600' ]]; then
	echo "proxy file permissions are not 600" >&2
	exit 1
fi

if [[ "$configure_output" == *"$secret"* ]]; then
	echo "configure-proxy.sh printed proxy credentials" >&2
	exit 1
fi

queries_file="$test_root/queries.txt"
output_dir="$test_root/output"
printf 'cafes in Nicosia\n' > "$queries_file"

run_output=$(bash "$script_dir/run-local.sh" \
	--queries "$queries_file" \
	--output-dir "$output_dir" \
	--proxy-file "$proxy_file" \
	--depth 1 \
	--dry-run)

if [[ "$run_output" == *"$secret"* ]]; then
	echo "run-local.sh printed proxy credentials" >&2
	exit 1
fi

if [[ "$run_output" != *"/run/secrets/gmaps-proxies"* ]]; then
	echo "run-local.sh did not mount the proxy file at the expected path" >&2
	exit 1
fi

if [[ "$run_output" != *"-proxies-file"* ]]; then
	echo "run-local.sh did not pass -proxies-file" >&2
	exit 1
fi

docker_log="$test_root/docker.log"
cat > "$fake_bin/docker" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "$DOCKER_TEST_LOG"

if [[ "${1:-}" == "inspect" ]]; then
	exit 1
fi

if [[ "${1:-}" == "pull" && "${DOCKER_PULL_FAIL:-}" == "1" ]]; then
	exit 1
fi
EOF
chmod +x "$fake_bin/docker"

export DOCKER_TEST_LOG="$docker_log"

PATH="$fake_bin:$PATH" bash "$script_dir/run-local.sh" \
	--queries "$queries_file" \
	--output-dir "$output_dir" \
	--depth 1 >/dev/null

if [[ $(grep -c '^pull gosom/google-maps-scraper$' "$docker_log") -ne 1 ]]; then
	echo "run-local.sh did not check for the latest Docker image" >&2
	exit 1
fi

: > "$docker_log"
PATH="$fake_bin:$PATH" bash "$script_dir/run-local.sh" \
	--queries "$queries_file" \
	--output-dir "$output_dir" \
	--depth 1 \
	--skip-image-pull >/dev/null

if grep -q '^pull ' "$docker_log"; then
	echo "run-local.sh pulled the Docker image after --skip-image-pull" >&2
	exit 1
fi

: > "$docker_log"
if ! DOCKER_PULL_FAIL=1 PATH="$fake_bin:$PATH" bash "$script_dir/run-local.sh" \
	--queries "$queries_file" \
	--output-dir "$output_dir" \
	--depth 1 >/dev/null 2>&1; then
	echo "run-local.sh blocked an offline run despite an available local image" >&2
	exit 1
fi

if [[ $(grep -c '^pull gosom/google-maps-scraper$' "$docker_log") -ne 2 ]]; then
	echo "run-local.sh did not retry a failed Docker image update check once" >&2
	exit 1
fi

skill_file="$script_dir/../SKILL.md"
required_references=(
	"references/query-planning.md"
	"references/proxy-setup.md"
	"references/local-execution.md"
	"references/recovery.md"
	"references/results.md"
	"references/advanced-coverage.md"
)

for reference in "${required_references[@]}"; do
	if ! grep -Fq "$reference" "$skill_file"; then
		echo "SKILL.md does not link $reference" >&2
		exit 1
	fi
done

if [[ $(grep -Fc 'select-proxy-sponsors.mjs' "$skill_file") -ne 1 ]]; then
	echo "SKILL.md must invoke the sponsor selector exactly once per setup" >&2
	exit 1
fi

if ! grep -Fiq 'sponsor the project' "$skill_file"; then
	echo "SKILL.md does not require sponsorship disclosure" >&2
	exit 1
fi

if ! grep -Fq -- '-proxies-file' "$skill_file"; then
	echo "SKILL.md does not use -proxies-file" >&2
	exit 1
fi

if grep -Fq 'suggest [Webshare]' "$skill_file"; then
	echo "SKILL.md still hard-codes a preferred proxy provider" >&2
	exit 1
fi

if [[ $(grep -Fc 'ensure-latest.sh' "$skill_file") -ne 1 ]]; then
	echo "SKILL.md must run the skill update helper exactly once per workflow" >&2
	exit 1
fi

if ! grep -Fq -- '--skip-image-pull' "$skill_file"; then
	echo "SKILL.md does not avoid a redundant Docker pull after validation" >&2
	exit 1
fi

if ! grep -Fiq 'do not ask for conversational permission' "$skill_file"; then
	echo "SKILL.md does not prevent unnecessary permission questions" >&2
	exit 1
fi

echo "agent helper tests passed"
