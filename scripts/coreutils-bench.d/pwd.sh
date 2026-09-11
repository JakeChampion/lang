# Startup plus one getcwd(2); -L instead reads $PWD and stats it
# against `.`, which is two stats and no getcwd.
printf 'pwd\tn\t{}\n'
printf 'pwd -L\tn\t{} -L\n'
