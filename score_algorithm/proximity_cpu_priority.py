import json
import math

COST_MODEL = {
    "SAME_NODE": 0.1,
    "SAME_RACK": 1.0,
    "SAME_AZ": 5.0,
    "CROSS_AZ": 20.0
}

# The threshold where we start aggressively penalizing CPU
CPU_WARNING_THRESHOLD = 80.0 

def calculate_distance_multiplier(candidate_node, target_node_name, topology_map):
    target_node = topology_map.get(target_node_name)
    if not target_node: return COST_MODEL["CROSS_AZ"] 
        
    if candidate_node["name"] == target_node["name"]: return COST_MODEL["SAME_NODE"]
    elif candidate_node["rack"] == target_node["rack"]: return COST_MODEL["SAME_RACK"]
    elif candidate_node["zone"] == target_node["zone"]: return COST_MODEL["SAME_AZ"]
    else: return COST_MODEL["CROSS_AZ"]

def calculate_cpu_penalty(cpu_pct):
    """
    Exponential penalty: Stays near 1.x when healthy, spikes sharply after 80%.
    Using the formula: 1 + e^((cpu - 80) / 10)
    """
    exponent = (cpu_pct - CPU_WARNING_THRESHOLD) / 10.0
    # Cap the exponent to prevent math overflow on crazy edge cases
    exponent = min(max(exponent, -10), 10) 
    return 1.0 + math.exp(exponent)

def score_nodes(state_file):
    with open(state_file, 'r') as f:
        state = json.load(f)
        
    candidate_nodes = state["candidate_nodes"]
    dependencies = state["traffic_dependencies"]
    topology_map = {node["name"]: node for node in candidate_nodes}
    
    node_scores = []
    
    print(f"--- SCHEDULING RUN: {state['pod_to_schedule']} ---")
    print("Algorithm: LOAD-PENALIZED (Network + CPU)\n")
    
    for candidate in candidate_nodes:
        raw_network_cost = 0.0
        
        # 1. Calculate raw network distance
        for dep in dependencies:
            distance_multiplier = calculate_distance_multiplier(candidate, dep["current_node"], topology_map)
            raw_network_cost += dep["bytes_per_second"] * distance_multiplier
            
        # 2. Calculate the CPU penalty
        cpu = candidate["cpu_utilization_pct"]
        penalty_multiplier = calculate_cpu_penalty(cpu)
        
        # 3. Final Score
        final_cost = raw_network_cost * penalty_multiplier
            
        node_scores.append({
            "node": candidate["name"],
            "score": final_cost,
            "raw_network": raw_network_cost,
            "cpu": cpu,
            "penalty": penalty_multiplier
        })
        
        print(f"Node: {candidate['name']:<10} | CPU: {cpu:>4.1f}% | Penalty: {penalty_multiplier:>4.2f}x | Final Cost: {final_cost:>8.1f}")

    # Sort nodes by lowest cost
    node_scores.sort(key=lambda x: x["score"])
    
    print("\n--- FINAL PLACEMENT DECISION ---")
    winner = node_scores[0]
    print(f"🥇 WINNER: {winner['node']}")
    print(f"Saved from hotspot! Node CPU is a safe {winner['cpu']}%.")
    print("----------------------------------------\n")

if __name__ == "__main__":
    score_nodes('mock_state.json')