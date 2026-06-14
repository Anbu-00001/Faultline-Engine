//! Ripple graph engine.
//!
//! Orbit's query DSL is capped at 3 hops with no transitive closure, so deep
//! call chains cannot be analyzed by the API alone. This engine ingests a
//! normalized code subgraph (assembled by the Go agent from bounded Orbit
//! queries) and computes the *full* transitive change-impact ("blast radius").
//!
//! Semantics: a `CALLS` edge `A -> B` means "A calls B". Changing B therefore
//! affects everything that transitively calls B, so the blast radius of a
//! changed definition is its transitive set of callers (reverse reachability).

use std::collections::{HashMap, HashSet, VecDeque};
use std::env;
use std::fs;

use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, Deserialize)]
struct Node {
    id: String,
    #[serde(default)]
    name: String,
    #[serde(default)]
    file_path: String,
    #[serde(default)]
    definition_type: String,
}

#[derive(Debug, Deserialize)]
struct Edge {
    #[serde(default, rename = "type")]
    etype: String,
    from: String,
    to: String,
}

#[derive(Debug, Deserialize)]
struct Graph {
    nodes: Vec<Node>,
    edges: Vec<Edge>,
}

#[derive(Debug, Serialize)]
struct Impacted {
    id: String,
    name: String,
    file_path: String,
    distance: u32,
}

#[derive(Debug, Serialize)]
struct Report {
    changed: Vec<String>,
    impacted_count: usize,
    max_depth: u32,
    blast_radius: Vec<Impacted>,
}

/// Compute the transitive caller set (blast radius) of the changed definitions.
fn analyze(graph: &Graph, changed: &[String]) -> Report {
    let node_by_id: HashMap<&str, &Node> =
        graph.nodes.iter().map(|n| (n.id.as_str(), n)).collect();

    // Reverse adjacency: callee -> [callers]. Treat empty edge type as CALLS.
    let mut callers: HashMap<&str, Vec<&str>> = HashMap::new();
    for e in &graph.edges {
        if e.etype == "CALLS" || e.etype.is_empty() {
            callers.entry(e.to.as_str()).or_default().push(e.from.as_str());
        }
    }

    let mut dist: HashMap<&str, u32> = HashMap::new();
    let mut queue: VecDeque<&str> = VecDeque::new();
    for c in changed {
        if let Some(n) = node_by_id.get(c.as_str()) {
            if dist.insert(n.id.as_str(), 0).is_none() {
                queue.push_back(n.id.as_str());
            }
        }
    }

    while let Some(cur) = queue.pop_front() {
        let d = dist[cur];
        if let Some(cs) = callers.get(cur) {
            for &caller in cs {
                if !dist.contains_key(caller) {
                    dist.insert(caller, d + 1);
                    queue.push_back(caller);
                }
            }
        }
    }

    let changed_set: HashSet<&str> = changed.iter().map(|s| s.as_str()).collect();
    let mut blast: Vec<Impacted> = dist
        .iter()
        .filter(|(id, _)| !changed_set.contains(**id))
        .filter_map(|(id, d)| {
            node_by_id.get(id).map(|n| Impacted {
                id: n.id.clone(),
                name: n.name.clone(),
                file_path: n.file_path.clone(),
                distance: *d,
            })
        })
        .collect();
    blast.sort_by(|a, b| a.distance.cmp(&b.distance).then(a.name.cmp(&b.name)));

    let max_depth = blast.iter().map(|x| x.distance).max().unwrap_or(0);
    Report {
        changed: changed.to_vec(),
        impacted_count: blast.len(),
        max_depth,
        blast_radius: blast,
    }
}

fn main() {
    let args: Vec<String> = env::args().collect();
    let mut graph_path = String::new();
    let mut changed_arg = String::new();
    let mut i = 1;
    while i < args.len() {
        match args[i].as_str() {
            "--graph" => {
                graph_path = args.get(i + 1).cloned().unwrap_or_default();
                i += 2;
            }
            "--changed" => {
                changed_arg = args.get(i + 1).cloned().unwrap_or_default();
                i += 2;
            }
            _ => i += 1,
        }
    }

    if graph_path.is_empty() {
        eprintln!("usage: ripple-engine --graph <graph.json> --changed <id,id,...>");
        std::process::exit(2);
    }

    let data = fs::read_to_string(&graph_path)
        .unwrap_or_else(|e| panic!("failed to read {graph_path}: {e}"));
    let graph: Graph = serde_json::from_str(&data).expect("invalid graph JSON");
    let changed: Vec<String> = changed_arg
        .split(',')
        .map(|s| s.trim().to_string())
        .filter(|s| !s.is_empty())
        .collect();

    let report = analyze(&graph, &changed);
    println!("{}", serde_json::to_string_pretty(&report).unwrap());
}

#[cfg(test)]
mod tests {
    use super::*;

    // Mirrors the verified ripple-demo-go calc graph:
    //   TotalWithTax -> CalculateTax -> {applyRate, standardRate}
    //   ApplyDiscount is isolated (uncalled).
    fn sample() -> Graph {
        let n = |id: &str, name: &str, f: &str| Node {
            id: id.into(),
            name: name.into(),
            file_path: f.into(),
            definition_type: "Function".into(),
        };
        let e = |from: &str, to: &str| Edge {
            etype: "CALLS".into(),
            from: from.into(),
            to: to.into(),
        };
        Graph {
            nodes: vec![
                n("A", "applyRate", "calc/tax.go"),
                n("S", "standardRate", "calc/tax.go"),
                n("C", "CalculateTax", "calc/tax.go"),
                n("T", "TotalWithTax", "calc/order.go"),
                n("D", "ApplyDiscount", "calc/tax.go"),
            ],
            edges: vec![e("T", "C"), e("C", "A"), e("C", "S")],
        }
    }

    #[test]
    fn blast_radius_of_applyrate_is_transitive() {
        let r = analyze(&sample(), &["A".to_string()]);
        let names: HashSet<&str> = r.blast_radius.iter().map(|x| x.name.as_str()).collect();
        assert!(names.contains("CalculateTax"), "direct caller missing");
        assert!(names.contains("TotalWithTax"), "transitive caller missing");
        assert_eq!(r.impacted_count, 2);
        assert_eq!(r.max_depth, 2);
    }

    #[test]
    fn uncalled_function_has_empty_blast_radius() {
        let r = analyze(&sample(), &["D".to_string()]);
        assert_eq!(r.impacted_count, 0);
        assert_eq!(r.max_depth, 0);
    }

    #[test]
    fn distances_are_shortest() {
        let r = analyze(&sample(), &["A".to_string()]);
        let by_name: HashMap<&str, u32> =
            r.blast_radius.iter().map(|x| (x.name.as_str(), x.distance)).collect();
        assert_eq!(by_name["CalculateTax"], 1);
        assert_eq!(by_name["TotalWithTax"], 2);
    }
}
