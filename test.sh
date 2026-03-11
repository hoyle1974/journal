#!/bin/bash
#
# Bulk test: send a sequence of entries and trigger dreams at milestones.
# Usage: ./test.sh <dev|prod>
# Environment must be explicit (no default). Script will confirm before continuing.
#
set -e

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$REPO_ROOT"
source "$REPO_ROOT/scripts/lib/env-confirm.sh"
require_env_and_confirm "$1"
shift

# Run jot with the chosen profile
pjot() {
  JOT_PROFILE="$ENV_TARGET" ./jot "$@"
}

# The dataset for Alex Reed
ENTRIES=(
"Hi, my name is Alex Reed and I'm a Lead Engineer at TechFlow."
"I live with my wife Sarah and our golden retriever, Buster."
"Starting a new work initiative today called Project Aegis."
"Project Aegis is a migration from REST to GraphQL for our core services."
"I need to schedule a kickoff meeting for Project Aegis on Wednesday."
"Sarah mentioned she wants to build a redwood deck in the backyard this summer."
"I feel a bit overwhelmed by the migration plan but excited to start."
"[Task] Research redwood prices at the local hardware store."
"Had a mediocre coffee at the office today."
"Met with Jamie, the new DevOps lead, to discuss Aegis infrastructure requirements."
"Jamie prefers Terraform over Pulumi for our IaC."
"I need to buy more dog food for Buster this evening."
"What is my role at TechFlow?"
"Who is the DevOps lead I met today?"
"I am feeling much more confident about the GraphQL schema design now."
"Sarah’s birthday is May 14th and I should probably plan a surprise."
"[Task] Draft the initial GraphQL schema for the User service."
"Completed the dog food errand; Buster is happy."
"The weather is surprisingly grey and gloomy for March."
"I noticed the backyard fence has a loose board that needs fixing."
"I need to call the plumber about the slow leak in the guest bathroom tomorrow."
"Project Aegis kickoff is confirmed for 10 AM on Wednesday."
"I want to use the deck project as a way to learn more about woodworking."
"[Task] Prepare the slide deck for the Aegis kickoff."
"Sarah prefers a wrap-around style for the redwood deck."
"What does Sarah want for the backyard?"
"[Task] Fix the loose board on the backyard fence."
"Had a great sync with Jamie; we are sticking with Terraform."
"I forgot to mention that Project Aegis has a hard deadline of June 1st."
"The guest bathroom leak is getting worse; I really need to call that plumber."
"I am reading a book on advanced concurrency in Go."
"Finished the slide deck for the kickoff tomorrow."
"[Update Task] Slide deck for Aegis kickoff is Completed."
"I’m thinking about using a charcoal stain for the deck to match the house trim."
"[Task] Order a new set of drill bits for the deck project."
"It's Wednesday morning; heading into the Aegis kickoff meeting now."
"Kickoff went great, but the team is worried about the June 1st deadline."
"[Task] Break down the Aegis migration into two-week milestones."
"What is the deadline for Project Aegis?"
"I spoke to the plumber; he's coming by Friday at 2 PM."
"[Task] Clear out the area under the guest bathroom sink before Friday."
"Buster needs to go to the vet for his annual shots next Tuesday."
"[Task] Book the vet appointment for Buster."
"Sarah says we should invite the neighbors over once the deck is done."
"The neighbors are Mark and Elena."
"[Task] Ask Mark if he still has that power sander I can borrow."
"I am feeling exhausted after a long day of architecture reviews."
"What are my tasks for the backyard?"
"I found a great deal on redwood 4x4s at a shop in San Leandro."
"[Update Task] Research redwood prices is Completed."
"I need to make sure the guest bathroom sink is actually clear for the plumber."
"Jamie suggests we use a canary deployment strategy for the migration."
"[Task] Write a document on the Aegis canary deployment strategy."
"I prefer morning runs to clear my head before work."
"I ran 5 miles today and felt strong."
"[Update Task] Clear out area under sink is Completed."
"The plumber arrived; the leak was just a worn-out O-ring."
"Plumber’s name was Gary and he was very efficient."
"[Task] Pay Gary the plumber for the O-ring fix."
"What did I do for the guest bathroom?"
"I’m starting to think the June 1st deadline is too aggressive for Aegis."
"[Task] Talk to the CTO about potentially moving the Aegis deadline to June 15th."
"Sarah bought some outdoor string lights for the future deck."
"[Task] Buy a gift for Sarah’s birthday."
"[Update Task] Fix the backyard fence board is Completed."
"I need to renew my driver's license by the end of the month."
"[Task] Schedule a DMV appointment for the license renewal."
"Who are my neighbors?"
"Mark said I can borrow his power sander whenever I'm ready."
"[Update Task] Ask Mark about power sander is Completed."
"Buster’s vet appointment is confirmed for Tuesday at 9 AM."
"I am worried about the cost of the redwood; it's up 20% since last year."
"[Task] Calculate the total lumber cost for a 12x16 deck."
"[Update Task] Draft GraphQL schema is Completed."
"What is the status of my work tasks?"
"Sarah’s sister, Mindy, is coming to visit in April."
"Mindy is allergic to cats, but luckily we only have Buster."
"I need to clean the guest room before Mindy arrives."
"[Task] Deep clean the guest room for Mindy's visit."
"Completed the canary deployment document for Jamie."
"[Update Task] Write Aegis deployment doc is Completed."
"The CTO agreed to push the Aegis deadline to June 15th."
"[Update Knowledge] Project Aegis deadline is now June 15th."
"What is the new deadline for Project Aegis?"
"I bought a high-end espresso machine today; no more mediocre office coffee."
"The espresso machine is a Breville Bambino."
"I need to buy descaling powder for the Breville eventually."
"[Task] Buy descaling powder for the Breville."
"It's Tuesday; Buster is at the vet getting his shots."
"[Update Task] Vet appointment for Buster is Completed."
"The vet said Buster is in perfect health but needs to watch his weight."
"I need to reduce Buster's daily kibble by 10 percent."
"[Task] Adjust Buster's feeding schedule."
"Sarah’s surprise birthday gift is a weekend trip to Napa."
"I need to book the hotel in Napa for May 15th-17th."
"[Task] Book Napa hotel for Sarah's birthday trip."
"What have I planned for Sarah's birthday?"
"[Update Task] Pay Gary the plumber is Completed."
"I feel like I'm finally on top of both my work and home projects."
"Summarize everything I've done this week regarding Project Aegis."
)

echo "Starting Bulk Test with Incremental Dreaming..."
echo "-----------------------------------"

for i in "${!ENTRIES[@]}"; do
  ENTRY="${ENTRIES[$i]}"
  COUNT=$((i + 1))
  
  echo "[$COUNT/100] Sending: $ENTRY"
  pjot query "$ENTRY"
  
  # Trigger Dreamer at specific milestones
  if [ $COUNT -eq 30 ]; then
    echo ">>> Milestone 1: Triggering Mid-Test Dream (Initial Persona Synthesis)..."
    pjot dream
  elif [ $COUNT -eq 70 ]; then
    echo ">>> Milestone 2: Triggering Mid-Test Dream (Task/Project Linking)..."
    pjot dream
  fi

  sleep 15
done

echo "-----------------------------------"
echo ">>> Final Milestone: Triggering Final Dream..."
pjot dream

echo "Test Sequence Finished."
