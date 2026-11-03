# AGENTS.compliance.md: Legal Compliance, Data Sovereignty, and Regulatory Discipline

This document defines mandatory data protection, cookie tracking synchronization, and
statutory legal boundaries across all Jad Madi projects.

## 1. Tracking and Cookie Synchronization

When introducing client-side storage or tracking scripts, follow these strict rules:

* Cookie Notice Updates: Whenever you add new client cookies, localStorage keys, or
  analytics pixels (such as PostHog, Google Analytics, or Datadog), update the project
  cookie notice in the same commit.
* Consent Separation: Separate strictly necessary authentication and session cookies
  from optional analytics and marketing cookies.
* Zero Pre-Consent Tracking: Never fire tracking tags or store non-essential cookies
  before the user grants explicit affirmative consent.
* Equal Prominence: Make sure consent banners give equal visual prominence to Reject
  All and Accept All buttons. Never use dark patterns or obscured rejection flows.

## 2. Telemetry and Data Protection

* Zero Silent Telemetry: Never introduce unconsented background analytics, phone-home
  beacons, or periodic tracking calls. Sila core is 100% local-first SQLite.
* Payload Redaction: Sanitize all outbound telemetry and crash reporting. Scrub user
  email addresses, IP addresses, credentials, auth tokens, and local machine paths.
* Retention Limits: Define explicit retention schedules for logs and edge mailboxes.
  Purge developer conversation transcripts upon project reset or user deletion.

## 3. Statutory Legal Invariants and Dispute Resolution

* No Unilateral Legal Edits: Coding agents must never modify, weaken, or rewrite
  arbitration agreements, class-action waivers, or governing law clauses without
  direct human legal counsel approval.
* California Civil Code Section 1542: Always preserve express statutory waivers in
  general release clauses for California consumers and counterparties.
* WCAG Accessibility: Maintain explicit WCAG 2.1 AA digital accessibility commitments
  in all user-facing website terms of use.
* State Disclosures: Include multi-state privacy disclosures (California CPRA, Colorado
  CPA, Virginia VCDPA, Connecticut CTDPA, Nevada 603A) in public privacy policies.

## 4. Third-Party Integrations and Health Data Boundaries

* Data Processing Agreements: When connecting third-party AI models or cloud APIs that
  receive user data, make sure valid GDPR Article 28 DPA terms are in place.
* HIPAA and PHI Safeguards: Never send Protected Health Information (PHI) to third-party
  APIs or cloud services without a formal Business Associate Agreement (BAA) under
  45 CFR 164.504(e) and verified encryption in transit.
* Subcontractor Flow-Down: Make sure all downstream sub-processors follow identical
  data protection and breach notification standards.
