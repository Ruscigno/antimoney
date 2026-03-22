## ## Phase 1: The "Financial Core" (Mathematical Foundation)

The first step is to port the exact-rational-number logic from C/Scheme to the backend. This ensures the web version maintains the uncompromising accuracy of the desktop original.

### ### 1.1 Precision Arithmetic Engine

* **Rational Representation:** Implement a library where every value is stored as a numerator/denominator pair ($num/denom$) to eliminate floating-point rounding errors.


* **Rounding Policies:** Replicate the `gnc_numeric` flags, specifically **Banker’s Rounding** (`GNC_HOW_RND_ROUND`) for neutral statistical drift and `GNC_HOW_RND_NEVER` for strict balance enforcement.


* **Unit Testing:** Certify the core with "Golden Data" sets—importing legacy GnuCash XML/SQLite files and asserting that calculated balances match to the penny.



### ### 1.2 Modernized Data Schema (GCP Cloud SQL)

* **Database:** Use **Cloud SQL for PostgreSQL** (db-f1-micro) for relational integrity.


* **JSONB for Extensibility:** Replace the legacy `slots` table with native **JSONB columns** in the `accounts` and `transactions` tables to eliminate the $N+1$ query performance bottleneck.


* **Concurrency Control:** Discard the `gnclock` file-locking system. Implement **Optimistic Concurrency Control (OCC)** using row versioning to allow multiple simultaneous users without data loss.



---

## ## Phase 2: The "Accounting Engine" (Backend Logic)

With the math and storage ready, the engine must enforce the laws of double-entry accounting.

### ### 2.1 Atomic Transaction Gateway

* **Aggregate Roots:** The API must treat a **Transaction** and its **Splits** as a single unit.


* **Zero-Sum Invariant:** Every commit must verify that $\sum \text{values} = 0$.


* **Validation Testing:** Automated integration tests must attempt to post "unbalanced" transactions and verify that the API returns a `422 Unprocessable Entity` or triggers the **Imbalance Scrubbing** routine.



### ### 2.2 Asynchronous Scrubbing Pipeline

* **Orphan & Imbalance Repair:** Use **Cloud Run Jobs** to run background "scrubbing" that fixes data inconsistencies, such as re-linking orphan splits or generating imbalance accounts.


* **Temporal Normalization:** Automatically shift `date_posted` timestamps to **11:00 UTC** to prevent date-drift across international time zones.



---

## ## Phase 3: The "Account Register" (Frontend Experience)

The UI must provide the high-density efficiency of a desktop application within a browser.

### ### 3.1 Reactive Ledger Views

* **Virtualization:** Use `react-window` to render thousands of rows in "Transaction Journal" mode without performance degradation.


* **View Logic Transformation:** Implement memoized selectors to toggle between **Basic Ledger** (one line), **Auto-Split** (expanded active row), and **Journal** views.


* **User Experience (UX) Masking:** Automatically flip positive/negative signs based on `account_type` so users can work with friendly "Deposits/Withdrawals" while the backend stores strict debits/credits.



---

## ## Phase 4: Quality Certification & CI/CD

A robust pipeline ensures that "improvements" don't break the accounting logic.

* **CI/CD Pipeline (Cloud Build):** 1.  **Stage 1:** Unit tests for rational-math and lot-assignment logic.
2.  **Stage 2:** Integration tests using a ephemeral PostgreSQL container to verify schema migrations.
3.  **Stage 3:** E2E tests for the "Invoice Posting" state machine to ensure business ledgers remain immutable.


* **Regression Testing:** Maintain a suite of complex multi-currency transactions and "Lot Scrubber" scenarios to ensure FIFO capital gains calculations remain accurate over time.



---

## ## Phase 5: GCP Low-Cost Deployment Strategy

| Layer | Component | GCP Product | Cost / Free Tier |
| --- | --- | --- | --- |
| **Frontend** | Static SPA | **Firebase Hosting** | Free tier (includes SSL/CDN) |
| **Backend** | API Services | **Cloud Run** | First 180k vCPU-seconds free/mo |
| **Database** | SQL Storage | **Cloud SQL** | ~$9-10/mo for `db-f1-micro` |
| **Tasks** | Scrubbing/STX | **Cloud Tasks** | Free for first 1M operations/mo |
| **Secrets** | Keys/Configs | **Secret Manager** | $0.03 per active secret/mo |
