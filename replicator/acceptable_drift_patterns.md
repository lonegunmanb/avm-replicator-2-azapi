# Acceptable Drift Patterns for AzureRM to AzAPI Migration

This document defines acceptable drift patterns when migrating from AzureRM provider resources to AzAPI provider resources using moved blocks. These patterns apply to all resource types during state migration.

## 🚨 MANDATORY EVALUATION PROCESS - READ THIS FIRST

**Before evaluating ANY drift as acceptable or unacceptable:**

1. ⛔ **IGNORE** all semantic meaning, prior test results, and your intuition about the change
2. ⛔ **IGNORE** whether the change seems "critical", "security-related", or "breaking"
3. ✅ **ONLY** follow the 3-step verification process in Section 2 below
4. ✅ **ONLY** after completing all 3 steps, make your judgment

**Common mistakes to avoid:**
- ❌ Judging based on change type (true->false, value->null, etc.) before verification
- ❌ Treating security-related or ForceNew fields as "special cases" 
- ❌ Letting previous test failure descriptions influence your evaluation
- ✅ The ONLY thing that matters: Was the field explicitly set in azurerm.tf?

## ⚠️ CRITICAL: Whitelist Approach

**This document uses a WHITELIST approach**: 

- ✅ **ONLY** the drift patterns explicitly listed below as "acceptable" are considered acceptable
- ❌ **ANY** drift pattern NOT explicitly listed in this document is considered UNACCEPTABLE and indicates a module implementation error
- When evaluating test results, if a drift doesn't match any pattern described in sections 1-3 below, it must be treated as a failure

## Acceptable Drift Patterns

### 1. AzAPI Resource-Level Attribute Changes

**⚠️ IMPORTANT**: ANY changes to these attributes are acceptable, regardless of from/to values.

✅ **Always acceptable attributes**:
- `ignore_null_property` - AzAPI behavior setting
- `locks` - Resource lock configuration
- `replace_triggers_external_values` - Module replacement tracking
- `sensitive_body_version` - Sensitive data versioning
- `output` - Computed output values
- `type` - API version changes (e.g., `@2025-04-01` → `@2024-11-01`)

**Rationale**: These are AzAPI provider implementation details that don't affect resource functionality. API version changes are expected as AzureRM and AzAPI may use different API versions for the same resource type.

### 2. Body Structure - Computed Field Changes (Azure API Defaults)

**⚠️ CRITICAL RULE**: When you see ANY change in the `body` attribute for a field, you MUST follow this verification process:

**Step 1: Check the AzureRM Provider Schema**
- Look up the field in the AzureRM provider documentation or schema
- Determine if it has `Optional: true` AND `Computed: true`

**Step 2: Check the Original Configuration**
- Open the `azurerm.tf` file and verify if this field was explicitly set
- If the field is NOT present in `azurerm.tf`, it was omitted (relying on Azure's default)

**Step 3: Apply the Acceptance Criteria**

✅ **Change is ACCEPTABLE if ALL of these are true**:
1. The field exists in AzureRM provider with `Optional: true` AND `Computed: true`, AND
2. The field was **omitted** (not explicitly specified) in the original `azurerm.tf` configuration

✅ **Change is ACCEPTABLE if**:
- The field does NOT exist in AzureRM provider schema at all

❌ **Change is NOT ACCEPTABLE if**:
- The field was **explicitly set** in `azurerm.tf` but shows a different value in the plan, OR
- The field is required or not marked as computed in AzureRM provider

**Why this happens**: When a field is optional+computed and omitted in configuration, Azure API decides its value. After migration, the module may pass different values or `null`, causing Azure to recompute. This is acceptable because the original configuration also delegated this decision to Azure.

**⚠️ CRITICAL: This Applies to ANY Value Change Pattern**

This rule applies regardless of what the destination value is:

- `value -> null` ✅ Acceptable if field is optional+computed and omitted in azurerm.tf
- `value1 -> value2` ✅ Acceptable if field is optional+computed and omitted in azurerm.tf  
- `true -> false` ✅ Acceptable if field is optional+computed and omitted in azurerm.tf
- `"string1" -> "string2"` ✅ Acceptable if field is optional+computed and omitted in azurerm.tf

**The key question is NOT "what is the destination value?" but rather "was this field explicitly configured in the original azurerm.tf?"**

**Examples**:

✅ **Acceptable**: `disablePasswordAuthentication = true -> null`
- Field is optional+computed in AzureRM provider
- Field is NOT present in azurerm.tf
- Azure originally computed it as `true`
- After migration, module passes `null`, Azure will recompute (likely `true` again)
- No behavior change occurs

❌ **NOT Acceptable**: `adminUsername = "admin" -> "root"`
- Field was explicitly set to `"admin"` in azurerm.tf
- Plan shows it changing to `"root"`
- This indicates a module bug regardless of field type


## ❌ Everything Else is UNACCEPTABLE

**Any drift pattern not matching sections 1-3 above indicates a module implementation error.**

Common examples of unacceptable drifts (non-exhaustive list):

1. **Explicitly Configured Values Differ**: Values that were explicitly set in configuration show different values in the plan
2. **Missing Required Fields**: Required fields are not populated correctly
3. **Resource Recreation**: Plan shows destroy/create instead of update/in-place change
4. **Wrong Resource Type**: Fundamental resource type mismatch
5. **Data Loss Risk**: Changes that would cause data loss or service interruption
6. **Unexpected Property Changes**: Any property change in `body` that doesn't match Pattern #2 criteria
