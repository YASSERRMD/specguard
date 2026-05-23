use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::collections::BTreeMap;
use std::ffi::{CStr, CString};
use std::os::raw::c_char;

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct OrdValue(pub serde_json::Value);

impl PartialEq for OrdValue {
    fn eq(&self, other: &Self) -> bool {
        self.0 == other.0
    }
}

impl Eq for OrdValue {}

impl PartialOrd for OrdValue {
    fn partial_cmp(&self, other: &Self) -> Option<std::cmp::Ordering> {
        Some(self.cmp(other))
    }
}

impl Ord for OrdValue {
    fn cmp(&self, other: &Self) -> std::cmp::Ordering {
        let s1 = serde_json::to_string(&self.0).unwrap_or_default();
        let s2 = serde_json::to_string(&other.0).unwrap_or_default();
        s1.cmp(&s2)
    }
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq, PartialOrd, Ord)]
pub struct Constraint {
    pub kind: String,
    pub value: String,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq, PartialOrd, Ord)]
pub struct Schema {
    #[serde(rename = "type")]
    pub schema_type: String, // "object", "array", "scalar", "enum"

    #[serde(default, skip_serializing_if = "BTreeMap::is_empty")]
    pub properties: BTreeMap<String, Schema>,

    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub required: Vec<String>,

    #[serde(skip_serializing_if = "Option::is_none")]
    pub item: Option<Box<Schema>>,

    #[serde(skip_serializing_if = "Option::is_none")]
    pub scalar_type: Option<String>,

    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub constraints: Vec<Constraint>,

    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub enum_values: Vec<String>,

    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub one_of: Vec<Schema>,

    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub any_of: Vec<Schema>,

    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub all_of: Vec<Schema>,

    #[serde(default, skip_serializing_if = "std::ops::Not::not")]
    pub nullable: bool,

    #[serde(skip_serializing_if = "Option::is_none")]
    pub additional_properties: Option<Box<Schema>>,

    #[serde(rename = "default", skip_serializing_if = "Option::is_none")]
    pub default_value: Option<OrdValue>,

    #[serde(skip_serializing_if = "Option::is_none")]
    pub example: Option<OrdValue>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Operation {
    pub id: String,
    pub input: Schema,
    pub output: Schema,
    #[serde(default, skip_serializing_if = "BTreeMap::is_empty")]
    pub error_shapes: BTreeMap<String, Schema>,
    #[serde(default, skip_serializing_if = "BTreeMap::is_empty")]
    pub metadata: BTreeMap<String, String>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct NormalizedSpec {
    pub operations: BTreeMap<String, Operation>,
}

#[derive(Serialize)]
struct StructuralOperation {
    id: String,
    input: Schema,
    output: Schema,
    error_shapes: BTreeMap<String, Schema>,
    method: String,
    path: String,
}

#[derive(Serialize)]
struct StructuralSpec {
    operations: BTreeMap<String, StructuralOperation>,
}

#[derive(Debug, Clone, Serialize)]
pub struct Finding {
    pub location: String,
    pub kind: String, // "missing", "added", "type-changed", "constraint-violated"
    pub expected: String,
    pub actual: String,
    pub severity: String, // "info", "warning", "error"
}

#[derive(Debug, Clone, Serialize)]
pub struct DriftReport {
    pub findings: Vec<Finding>,
}

fn to_structural_spec(spec: &NormalizedSpec) -> StructuralSpec {
    let mut operations = BTreeMap::new();
    for (op_id, op) in &spec.operations {
        let method = op.metadata.get("method").cloned().unwrap_or_default();
        let path = op.metadata.get("path").cloned().unwrap_or_default();
        operations.insert(
            op_id.clone(),
            StructuralOperation {
                id: op.id.clone(),
                input: op.input.clone(),
                output: op.output.clone(),
                error_shapes: op.error_shapes.clone(),
                method,
                path,
            },
        );
    }
    StructuralSpec { operations }
}

pub fn hash_spec_internal(spec: &NormalizedSpec) -> String {
    let structural = to_structural_spec(spec);
    let json_bytes = serde_json::to_vec(&structural).unwrap();
    let mut hasher = Sha256::new();
    hasher.update(&json_bytes);
    let result = hasher.finalize();
    format!("{:x}", result)
}

fn diff_schema(
    location: &str,
    expected: &Schema,
    actual: &Schema,
    findings: &mut Vec<Finding>,
) {
    if expected.nullable != actual.nullable {
        findings.push(Finding {
            location: format!("{}.nullable", location),
            kind: "constraint-violated".to_string(),
            expected: expected.nullable.to_string(),
            actual: actual.nullable.to_string(),
            severity: "error".to_string(),
        });
    }

    match (&expected.additional_properties, &actual.additional_properties) {
        (Some(exp_ap), Some(act_ap)) => {
            diff_schema(&format!("{}.additional_properties", location), exp_ap, act_ap, findings);
        }
        (Some(_), None) => {
            findings.push(Finding {
                location: format!("{}.additional_properties", location),
                kind: "missing".to_string(),
                expected: "additionalProperties schema".to_string(),
                actual: "nil".to_string(),
                severity: "error".to_string(),
            });
        }
        (None, Some(_)) => {
            findings.push(Finding {
                location: format!("{}.additional_properties", location),
                kind: "added".to_string(),
                expected: "nil".to_string(),
                actual: "additionalProperties schema".to_string(),
                severity: "info".to_string(),
            });
        }
        (None, None) => {}
    }

    if expected.one_of.len() != actual.one_of.len() {
        findings.push(Finding {
            location: format!("{}.one_of", location),
            kind: "constraint-violated".to_string(),
            expected: format!("oneOf len {}", expected.one_of.len()),
            actual: format!("oneOf len {}", actual.one_of.len()),
            severity: "error".to_string(),
        });
    } else {
        for (i, (exp_sub, act_sub)) in expected.one_of.iter().zip(actual.one_of.iter()).enumerate() {
            diff_schema(&format!("{}.one_of[{}]", location, i), exp_sub, act_sub, findings);
        }
    }

    if expected.any_of.len() != actual.any_of.len() {
        findings.push(Finding {
            location: format!("{}.any_of", location),
            kind: "constraint-violated".to_string(),
            expected: format!("anyOf len {}", expected.any_of.len()),
            actual: format!("anyOf len {}", actual.any_of.len()),
            severity: "error".to_string(),
        });
    } else {
        for (i, (exp_sub, act_sub)) in expected.any_of.iter().zip(actual.any_of.iter()).enumerate() {
            diff_schema(&format!("{}.any_of[{}]", location, i), exp_sub, act_sub, findings);
        }
    }

    if expected.all_of.len() != actual.all_of.len() {
        findings.push(Finding {
            location: format!("{}.all_of", location),
            kind: "constraint-violated".to_string(),
            expected: format!("allOf len {}", expected.all_of.len()),
            actual: format!("allOf len {}", actual.all_of.len()),
            severity: "error".to_string(),
        });
    } else {
        for (i, (exp_sub, act_sub)) in expected.all_of.iter().zip(actual.all_of.iter()).enumerate() {
            diff_schema(&format!("{}.all_of[{}]", location, i), exp_sub, act_sub, findings);
        }
    }

    if expected.schema_type != actual.schema_type {
        findings.push(Finding {
            location: location.to_string(),
            kind: "type-changed".to_string(),
            expected: expected.schema_type.clone(),
            actual: actual.schema_type.clone(),
            severity: "error".to_string(),
        });
        return;
    }

    match expected.schema_type.as_str() {
        "scalar" => {
            let exp_scalar = expected.scalar_type.as_deref().unwrap_or("");
            let act_scalar = actual.scalar_type.as_deref().unwrap_or("");
            if exp_scalar != act_scalar {
                findings.push(Finding {
                    location: format!("{}.scalar_type", location),
                    kind: "type-changed".to_string(),
                    expected: exp_scalar.to_string(),
                    actual: act_scalar.to_string(),
                    severity: "error".to_string(),
                });
            }

            let mut exp_constraints = BTreeMap::new();
            for c in &expected.constraints {
                exp_constraints.insert(&c.kind, &c.value);
            }
            let mut act_constraints = BTreeMap::new();
            for c in &actual.constraints {
                act_constraints.insert(&c.kind, &c.value);
            }

            for (kind, exp_val) in &exp_constraints {
                match act_constraints.get(kind) {
                    None => {
                        findings.push(Finding {
                            location: format!("{}.constraints.{}", location, kind),
                            kind: "constraint-violated".to_string(),
                            expected: exp_val.to_string(),
                            actual: "missing".to_string(),
                            severity: "error".to_string(),
                        });
                    }
                    Some(act_val) => {
                        if exp_val != act_val {
                            findings.push(Finding {
                                location: format!("{}.constraints.{}", location, kind),
                                kind: "constraint-violated".to_string(),
                                expected: exp_val.to_string(),
                                actual: act_val.to_string(),
                                severity: "error".to_string(),
                            });
                        }
                    }
                }
            }

            for (kind, act_val) in &act_constraints {
                if !exp_constraints.contains_key(kind) {
                    findings.push(Finding {
                        location: format!("{}.constraints.{}", location, kind),
                        kind: "added".to_string(),
                        expected: "".to_string(),
                        actual: act_val.to_string(),
                        severity: "info".to_string(),
                    });
                }
            }
        }
        "enum" => {
            let exp_set: std::collections::BTreeSet<_> = expected.enum_values.iter().collect();
            let act_set: std::collections::BTreeSet<_> = actual.enum_values.iter().collect();

            for val in &exp_set {
                if !act_set.contains(val) {
                    findings.push(Finding {
                        location: format!("{}.enum_values", location),
                        kind: "constraint-violated".to_string(),
                        expected: val.to_string(),
                        actual: "missing".to_string(),
                        severity: "error".to_string(),
                    });
                }
            }

            for val in &act_set {
                if !exp_set.contains(val) {
                    findings.push(Finding {
                        location: format!("{}.enum_values", location),
                        kind: "added".to_string(),
                        expected: "".to_string(),
                        actual: val.to_string(),
                        severity: "info".to_string(),
                    });
                }
            }
        }
        "array" => {
            match (&expected.item, &actual.item) {
                (Some(exp_item), Some(act_item)) => {
                    diff_schema(&format!("{}.item", location), exp_item, act_item, findings);
                }
                (Some(_), None) => {
                    findings.push(Finding {
                        location: format!("{}.item", location),
                        kind: "missing".to_string(),
                        expected: "array item schema".to_string(),
                        actual: "nil".to_string(),
                        severity: "error".to_string(),
                    });
                }
                (None, Some(_)) => {
                    findings.push(Finding {
                        location: format!("{}.item", location),
                        kind: "added".to_string(),
                        expected: "nil".to_string(),
                        actual: "array item schema".to_string(),
                        severity: "info".to_string(),
                    });
                }
                (None, None) => {}
            }
        }
        "object" => {
            for (prop_name, exp_prop) in &expected.properties {
                let prop_loc = format!("{}.properties.{}", location, prop_name);
                match actual.properties.get(prop_name) {
                    None => {
                        let is_required = expected.required.contains(prop_name);
                        let severity = if is_required { "error" } else { "warning" };
                        findings.push(Finding {
                            location: prop_loc,
                            kind: "missing".to_string(),
                            expected: "property exists".to_string(),
                            actual: "missing".to_string(),
                            severity: severity.to_string(),
                        });
                    }
                    Some(act_prop) => {
                        diff_schema(&prop_loc, exp_prop, act_prop, findings);
                    }
                }
            }

            for prop_name in actual.properties.keys() {
                if !expected.properties.contains_key(prop_name) {
                    findings.push(Finding {
                        location: format!("{}.properties.{}", location, prop_name),
                        kind: "added".to_string(),
                        expected: "".to_string(),
                        actual: format!("property {}", prop_name),
                        severity: "info".to_string(),
                    });
                }
            }
        }
        _ => {}
    }
}

pub fn diff_spec_internal(spec_a: &NormalizedSpec, spec_b: &NormalizedSpec) -> DriftReport {
    let mut findings = Vec::new();

    for (op_id, op_a) in &spec_a.operations {
        let op_loc = format!("operations.{}", op_id);
        match spec_b.operations.get(op_id) {
            None => {
                findings.push(Finding {
                    location: "operations".to_string(),
                    kind: "missing".to_string(),
                    expected: format!("operation {}", op_id),
                    actual: "missing".to_string(),
                    severity: "error".to_string(),
                });
            }
            Some(op_b) => {
                diff_schema(&format!("{}.input", op_loc), &op_a.input, &op_b.input, &mut findings);
                diff_schema(&format!("{}.output", op_loc), &op_a.output, &op_b.output, &mut findings);
                
                for (status, err_a) in &op_a.error_shapes {
                    let err_loc = format!("{}.error_shapes.{}", op_loc, status);
                    match op_b.error_shapes.get(status) {
                        None => {
                            findings.push(Finding {
                                location: format!("{}.error_shapes", op_loc),
                                kind: "missing".to_string(),
                                expected: format!("error status {}", status),
                                actual: "missing".to_string(),
                                severity: "warning".to_string(),
                            });
                        }
                        Some(err_b) => {
                            diff_schema(&err_loc, err_a, err_b, &mut findings);
                        }
                    }
                }
                for status in op_b.error_shapes.keys() {
                    if !op_a.error_shapes.contains_key(status) {
                        findings.push(Finding {
                            location: format!("{}.error_shapes", op_loc),
                            kind: "added".to_string(),
                            expected: "".to_string(),
                            actual: format!("error status {}", status),
                            severity: "info".to_string(),
                        });
                    }
                }
            }
        }
    }

    for op_id in spec_b.operations.keys() {
        if !spec_a.operations.contains_key(op_id) {
            findings.push(Finding {
                location: "operations".to_string(),
                kind: "added".to_string(),
                expected: "".to_string(),
                actual: format!("operation {}", op_id),
                severity: "info".to_string(),
            });
        }
    }

    DriftReport { findings }
}

/// Hashing a spec from a JSON C-string.
///
/// # Safety
///
/// The caller must ensure that `spec_json` points to a valid, null-terminated C string.
/// The returned pointer is owned by the caller and must be freed using `free_string`.
#[no_mangle]
pub unsafe extern "C" fn hash_spec(spec_json: *const c_char) -> *mut c_char {
    if spec_json.is_null() {
        return std::ptr::null_mut();
    }
    let c_str = CStr::from_ptr(spec_json);
    let spec_str = match c_str.to_str() {
        Ok(s) => s,
        Err(_) => return std::ptr::null_mut(),
    };

    let spec: NormalizedSpec = match serde_json::from_str(spec_str) {
        Ok(s) => s,
        Err(_) => return std::ptr::null_mut(),
    };

    let hash = hash_spec_internal(&spec);
    let c_string = CString::new(hash).unwrap();
    c_string.into_raw()
}

/// Diffing two specs from JSON C-strings.
///
/// # Safety
///
/// The caller must ensure that both `spec_a_json` and `spec_b_json` point to valid, null-terminated C strings.
/// The returned pointer is owned by the caller and must be freed using `free_string`.
#[no_mangle]
pub unsafe extern "C" fn diff_specs(spec_a_json: *const c_char, spec_b_json: *const c_char) -> *mut c_char {
    if spec_a_json.is_null() || spec_b_json.is_null() {
        return std::ptr::null_mut();
    }
    let c_str_a = CStr::from_ptr(spec_a_json);
    let c_str_b = CStr::from_ptr(spec_b_json);
    let spec_a_str = match c_str_a.to_str() {
        Ok(s) => s,
        Err(_) => return std::ptr::null_mut(),
    };
    let spec_b_str = match c_str_b.to_str() {
        Ok(s) => s,
        Err(_) => return std::ptr::null_mut(),
    };

    let spec_a: NormalizedSpec = match serde_json::from_str(spec_a_str) {
        Ok(s) => s,
        Err(_) => return std::ptr::null_mut(),
    };
    let spec_b: NormalizedSpec = match serde_json::from_str(spec_b_str) {
        Ok(s) => s,
        Err(_) => return std::ptr::null_mut(),
    };

    let report = diff_spec_internal(&spec_a, &spec_b);
    let report_json = serde_json::to_string(&report).unwrap();
    let c_string = CString::new(report_json).unwrap();
    c_string.into_raw()
}

/// Freeing a C-string returned by other FFI functions.
///
/// # Safety
///
/// The caller must ensure that `s` is a valid pointer allocated by `hash_spec` or `diff_specs` and has not been freed yet.
#[no_mangle]
pub unsafe extern "C" fn free_string(s: *mut c_char) {
    if !s.is_null() {
        let _ = CString::from_raw(s);
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_hash_spec_internal() {
        let spec_json = r#"{
            "operations": {
                "get_user": {
                    "id": "get_user",
                    "input": { "type": "object", "properties": {} },
                    "output": { "type": "object", "properties": {} }
                }
            }
        }"#;
        let spec: NormalizedSpec = serde_json::from_str(spec_json).unwrap();
        let h = hash_spec_internal(&spec);
        assert!(!h.is_empty());
    }

    #[test]
    fn test_diff_spec_internal() {
        let spec_a_json = r#"{
            "operations": {
                "get_user": {
                    "id": "get_user",
                    "input": { "type": "object", "properties": {} },
                    "output": { "type": "object", "properties": {} }
                }
            }
        }"#;
        let spec_b_json = r#"{
            "operations": {
                "get_user": {
                    "id": "get_user",
                    "input": {
                        "type": "object",
                        "properties": {
                            "name": { "type": "scalar", "scalar_type": "string" }
                        },
                        "required": ["name"]
                    },
                    "output": { "type": "object", "properties": {} }
                }
            }
        }"#;
        let spec_a: NormalizedSpec = serde_json::from_str(spec_a_json).unwrap();
        let spec_b: NormalizedSpec = serde_json::from_str(spec_b_json).unwrap();
        let report = diff_spec_internal(&spec_a, &spec_b);
        assert_eq!(report.findings.len(), 1);
        assert_eq!(report.findings[0].kind, "added");
    }
}

