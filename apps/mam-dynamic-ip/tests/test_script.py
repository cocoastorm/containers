"""Run the real script with fake HTTP/logging tools; no credentials or network."""
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

SCRIPT = Path(__file__).resolve().parents[1] / "scripts/script.sh"


class ScriptTest(unittest.TestCase):
    def run_script(self, gluetun=False, cached=False):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            cookie = root / "cookies"
            cache = root / "ip"
            if cached:
                cookie.write_text("fixture cookie\n")
                cache.write_text("203.0.113.42\n0\n")
            harness = r'''
            gum() { printf '%s\n' "$*"; }
            jq() {
              local input; input=$(cat)
              case "$*" in
                *public_ip*) printf '203.0.113.42\n' ;;
                *) printf '%s\n' "${MOCK_RESPONSE}" ;;
              esac
            }
            curl() {
              printf '%s\n' "$*" >> "$CALLS"
              case "${*: -1}" in
                https://ipinfo.io/ip) printf '203.0.113.42\n' ;;
                */v1/publicip/ip) printf '{"public_ip":"203.0.113.42"}\n' ;;
                https://t.myanonamouse.net/json/dynamicSeedbox.php)
                  printf 'fixture cookie\n' > "$MAM_SESSION_DIR"
                  printf '{"msg":"%s"}\n' "$MOCK_RESPONSE" ;;
                *) return 99 ;;
              esac
            }
            source "$SCRIPT"
            '''
            env = dict(os.environ, SCRIPT=str(SCRIPT),
                       MAM_SESSION_ID="synthetic-session-secret",
                       MAM_SESSION_DIR=str(cookie), IP_CACHE_FILE=str(cache),
                       GLUETUN_ENABLED=str(gluetun).lower(), LOG_TIMESTAMP="false",
                       GLUETUN_CONTROL_SERVER_HOST="localhost",
                       GLUETUN_CONTROL_SERVER_PORT="8000",
                       GLUETUN_CONTROL_SERVER_API_KEY="synthetic-api-key",
                       MOCK_RESPONSE="Completed", CALLS=str(root / "calls"))
            result = subprocess.run(["bash", "-c", harness], env=env,
                                    capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            logs = result.stdout + result.stderr
            self.assertNotIn(env["MAM_SESSION_ID"], logs)
            self.assertNotIn(env["GLUETUN_CONTROL_SERVER_API_KEY"], logs)
            calls = (root / "calls").read_text()
            if gluetun:
                self.assertIn("http://localhost:8000/v1/publicip/ip", calls)
                self.assertNotIn("ipinfo.io", calls)
            else:
                self.assertIn("https://ipinfo.io/ip", calls)
                self.assertNotIn("localhost", calls)
            if cached:
                self.assertNotIn("dynamicSeedbox.php", calls)
            else:
                self.assertIn("mam_id=synthetic-session-secret", calls)
                self.assertIn("-b " + str(cookie), calls)
                self.assertEqual(cache.read_text().splitlines()[0], "203.0.113.42")

    def test_direct_mode(self):
        self.run_script()

    def test_gluetun_mode(self):
        self.run_script(gluetun=True)

    def test_unchanged_ip(self):
        self.run_script(cached=True)


if __name__ == "__main__":
    unittest.main()
