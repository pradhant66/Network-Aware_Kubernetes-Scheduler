import json

# Define the network distance multipliers
COST_MODEL = {
    "SAME_NODE": 0.1,
    "SAME_RACK": 1.0,
    "SAME_AZ": 5.0,
    "CROSS_AZ": 20.0
}

def calculate_distance_multiplier(candidate_node, target_node_name, topology_map):
    """Determines the topological distance between the candidate and the target."""
    target_node = topology_map.get(target_node_name)
    
    # Fallback if the target node isn't in our candidate list for some reason
    if not target_node:
        return COST_MODEL["CROSS_AZ"] 
        
    if candidate_node["name"] == target_node["name"]:
        return COST_MODEL["SAME_NODE"]
    elif candidate_node["rack"] == target_node["rack"]:
        return COST_MODEL["SAME_RACK"]
    elif candidate_node["zone"] == target_node["zone"]:
        return COST_MODEL["SAME_AZ"]
    else:
        return COST_MODEL["CROSS_AZ"]

def score_nodes(state_file):
    with open(state_file, 'r') as f:
        state = json.load(f)
        
    candidate_nodes = state["candidate_nodes"]
    dependencies = state["traffic_dependencies"]
    
    # Build a quick lookup map for the current topology
    topology_map = {node["name"]: node for node in candidate_nodes}
    
    node_scores = []
    
    print(f"--- SCHEDULING RUN: {state['pod_to_schedule']} ---")
    print("Algorithm: NAIVE GREEDY (Network Only)\n")
    
    for candidate in candidate_nodes:
        total_network_cost = 0.0
        
        for dep in dependencies:
            distance_multiplier = calculate_distance_multiplier(candidate, dep["current_node"], topology_map)
            # Core formula: Traffic Volume * Distance Penalty
            edge_cost = dep["bytes_per_second"] * distance_multiplier
            total_network_cost += edge_cost
            
        # Notice how we ignore candidate["cpu_utilization_pct"] here for the naive version!
            
        node_scores.append({
            "node": candidate["name"],
            "score": total_network_cost,
            "cpu_ignored": candidate["cpu_utilization_pct"]
        })
        print(f"Node: {candidate['name']:<25} | Network Cost: {total_network_cost:>8.1f}")

    # Sort nodes by lowest cost (Best fit first)
    node_scores.sort(key=lambda x: x["score"])
    
    print("\n--- FINAL PLACEMENT DECISION ---")
    winner = node_scores[0]
    print(f"🥇 WINNER: {winner['node']}")
    print(f"Warning: Placed on a node with {winner['cpu_ignored']}% CPU utilization.")
    print("----------------------------------------\n")

if __name__ == "__main__":
    score_nodes('./mock_state.json')