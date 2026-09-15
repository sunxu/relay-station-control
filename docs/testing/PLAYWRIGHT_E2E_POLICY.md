# Playwright E2E Policy

## Status

Mandatory repository-wide policy.

Applies to Phase 7 and every subsequent phase unless changed through an explicit architecture/testing-policy review.

## Goals

- stable E2E selectors
- no DOM-order coupling
- no localization coupling for interaction locators
- no dependency on component-library internal DOM
- deterministic synchronization
- preserve accessibility/keyboard coverage
- prevent flaky tests from being hidden by retries or sleeps

## 1. Interaction Locator Rule

All Playwright interactions MUST locate their target through `getByTestId()`.

Allowed:

```ts
await page.getByTestId("account-upload-new").click();
await page.getByTestId(`relay-node-option-${nodeID}`).click();
await page.getByTestId("account-email").fill(email);
```

Forbidden for interactions:

```ts
page.getByText("Upload").click();
page.getByRole("button", { name: "Upload" }).click();
page.getByTitle("Node B").click();
page.locator(".ant-select-dropdown").click();
page.locator("xpath=...");
page.locator("button").nth(2).click();
```

## 2. Read-only Assertions

Semantic assertions are allowed when verifying visible user-facing output.

```ts
await expect(page.getByText("读取不可用（unavailable）", { exact: true })).toBeVisible();
```

Prefer scoping when repeated text is possible. These locators must not be reused for interaction.

## 3. Test ID Design

Test IDs form a stable testing contract. Prefer domain semantics.

```text
node-pagination-next
relay-node-selector
relay-node-option-<node-id>

account-upload-new
account-disable-<account-id>
account-enable-<account-id>
account-replace-<account-id>
account-remove-<account-id>

account-remove-confirm
account-remove-cancel

operation-lifecycle-override-<command-id>
operation-same-account-override-<command-id>

override-reason-selector
override-reason-option-<reason>
```

Do not encode DOM order or styling.

## 4. Third-party Components

For Ant Design or other component-library controls, do not target internal CSS classes or DOM structure. Expose stable `data-testid` hooks at the application's semantic interaction boundary.

If an option must be independently selected, expose a stable test ID for that option based on a stable domain identifier.

## 5. Keyboard Tests

Locator stability and interaction modality are separate concerns.

```ts
const selector = page.getByTestId("relay-node-selector");
await selector.focus();
await selector.press("ArrowDown");
await selector.press("Enter");
```

If the test requirement says keyboard selection, actual keyboard input must remain covered.

## 6. Synchronization

Use deterministic event/state synchronization.

```ts
await Promise.all([
  page.waitForResponse(/* exact relevant response */),
  page.getByTestId("node-pagination-next").click(),
]);
```

Where practical, parse URLs structurally rather than relying on loose substring matching. Avoid `page.waitForTimeout(5000)`. Do not increase Playwright retry counts merely to make a flaky test green.

## 7. Testability-only Production Changes

Adding `data-testid` attributes is allowed. Classify these changes as:

```text
testability-only production frontend changes
```

They must not change runtime business behavior.

## 8. Forbidden Testability Mechanisms

Do not add:

- E2E authentication bypasses
- test-only production APIs
- business-rule bypasses
- provider-policy bypasses
- database shortcuts exposed to production
- alternate E2E execution paths

The browser acceptance path must continue through the real production interfaces.

## 9. Review Checklist

For every new or modified Playwright E2E test:

- [ ] all interaction locators use `getByTestId()`
- [ ] no interactive `getByText()`
- [ ] no interactive `getByRole()`
- [ ] no interactive `getByTitle()`
- [ ] no interactive CSS/XPath
- [ ] no `.first()`, `.last()`, `.nth()`
- [ ] no DOM-order-dependent locator
- [ ] no `force: true`
- [ ] no arbitrary sleeps
- [ ] no retry increase used to hide flakiness
- [ ] keyboard coverage preserved where required
- [ ] test IDs use stable product/domain semantics
- [ ] testability-only changes do not alter business semantics

## 10. Scope

This policy is not limited to Phase 7.

It applies automatically to all subsequent phases and all newly created or modified Playwright E2E tests.

Historical tests do not need to be rewritten solely because this policy was introduced. However, when a historical test is modified or blocks a current phase, its interaction locators must be brought into compliance.
