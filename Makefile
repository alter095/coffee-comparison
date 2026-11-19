.PHONY: check e2e

check:
	bash scripts/check.sh

e2e: check
	bash scripts/e2e.sh
