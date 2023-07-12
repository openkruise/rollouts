#!/bin/bash

URL=$1
TIMES=$2
STRING1="v1" 
STRING2="v2"
COUNT1=0
COUNT2=0

for ((i=1; i<=$TIMES; i++))
do
    response=$(curl -s "$URL")
    if [[ $response == *"$STRING1"* ]] 
    then
        ((COUNT1++))
    elif [[ $response == *"$STRING2"* ]] 
    then
        ((COUNT2++))       
    fi
done

echo "Total query: $2"
echo "'$STRING1': $COUNT1"
echo "'$STRING2': $COUNT2"