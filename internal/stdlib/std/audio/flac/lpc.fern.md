# FLAC linear predictor analysis

This module selects integer linear predictors through order 32.
A Welch window and autocorrelation supply the Levinson-Durbin recursion.
Each stable order produces signed 15-bit coefficients and a nonnegative shift through 15.
An unstable or silent analysis can return no predictors.

Floating-point analysis selects coefficients only.
The frame planner computes exact integer residuals and compares complete encoded bit costs.
Analysis retains at most one float per source sample and bounded predictor arrays.

```fern
<<*>>=
// Analyze bounded FLAC samples and quantize reversible integer predictors.

pub struct Model { coefficients: i32[], shift: i32 }

function absolute(value: f64): f64 {
  return if (value < 0.0) { -value } else { value };
}

function window(values: [i64]): f64[] {
  let output: f64[] = [];
  let center = (values.len() - 1) as f64;
  for at in 0..values.len() {
    let position = (2.0 * (at as f64) - center) / center;
    output = output.append(values[at] as f64 * (1.0 - position * position));
  }
  return output;
}

function correlations(values: [f64], maximum: i32): f64[] {
  let output: f64[] = [];
  for lag in 0..maximum + 1 {
    let sum = 0.0;
    for at in lag..values.len() {
      sum += values[at] * values[at - lag];
    }
    output = output.append(sum);
  }
  return output;
}

function quantize(coefficients: [f64]): Option[Model] {
  let maximum = 0.0;
  for value in coefficients {
    if (absolute(value) > maximum) {
      maximum = absolute(value);
    }
  }
  if (maximum > 16383.0) {
    return None;
  }
  let scale = 32768.0;
  let shift = 15;
  while (maximum * scale > 16383.0 && shift > 0) {
    scale *= 0.5;
    shift -= 1;
  }
  let output: i32[] = [];
  let error = 0.0;
  for value in coefficients {
    let target = value * scale + error;
    let rounded = (if (target >= 0.0) { target + 0.5 } else { target - 0.5 }) as i32;
    if (rounded < -16384 || rounded > 16383) {
      return None;
    }
    output = output.append(rounded);
    error = target - rounded as f64;
  }
  return Some(Model { coefficients: output, shift: shift });
}

function reflection(correlation: [f64], coefficients: [f64], error: f64): f64 {
  let order = coefficients.len() + 1;
  let value = correlation[order];
  for at in 0..coefficients.len() {
    value -= coefficients[at] * correlation[order - 1 - at];
  }
  return value / error;
}

function extend(coefficients: [f64], reflection: f64): f64[] {
  let output: f64[] = [];
  for at in 0..coefficients.len() {
    output = output.append(coefficients[at] - reflection * coefficients[coefficients.len() - 1 - at]);
  }
  return output.append(reflection);
}

pub function models(values: [i64], maximum: i32): Model[] {
  if (values.len() < 2 || maximum < 1 || maximum > 32) {
    return [];
  }
  let order = if (maximum < values.len()) { maximum } else { values.len() - 1 };
  let correlation = correlations(window(values), order);
  let error = correlation[0];
  let coefficients: f64[] = [];
  let output: Model[] = [];
  for _ in 0..order {
    if (error <= correlation[0] * 0.000000000001 || error == 0.0) {
      break;
    }
    let value = reflection(correlation, coefficients, error);
    if (absolute(value) >= 1.0) {
      break;
    }
    coefficients = extend(coefficients, value);
    error *= 1.0 - value * value;
    if let Some(model) = quantize(coefficients) {
      output = output.append(model);
    }
  }
  return output;
}
```
